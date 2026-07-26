package hotkey

import (
	"errors"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"zee/log"
)

// captureTimeout bounds how long Capture waits for the user to press a chord.
const captureTimeout = 20 * time.Second

// DefaultLongPress separates the two press styles: held longer = push-to-talk
// (release stops), shorter = toggle (next tap stops).
const DefaultLongPress = 350 * time.Millisecond

// LongPress is the effective threshold: DefaultLongPress unless overridden by
// ZEE_LONGPRESS_DURATION (a Go duration, e.g. "500ms"). Every caller must use
// this rather than the constant — `zee doctor` claims to exercise "the app's
// own press semantics", which is only true if it reads the same override.
func LongPress() time.Duration {
	v := os.Getenv("ZEE_LONGPRESS_DURATION")
	if v == "" {
		return DefaultLongPress
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Warnf("invalid ZEE_LONGPRESS_DURATION %q, using default %s", v, DefaultLongPress)
		return DefaultLongPress
	}
	return d
}

// AwaitRecord waits (up to timeout) for the combo to start a recording and
// returns a stop channel driven by WaitStop — the exact press semantics of
// the app's record loop, packaged for callers with no session machinery
// (`zee doctor`). fired is false when the combo never came.
func AwaitRecord(hk Hotkey, longPress, timeout time.Duration) (stop <-chan struct{}, fired bool) {
	select {
	case <-hk.Keydown():
	case <-time.After(timeout):
		return nil, false
	}
	stopCh := make(chan struct{})
	go func() {
		WaitStop(hk, longPress, nil)
		close(stopCh)
	}()
	return stopCh, true
}

// WaitStop implements zee's press semantics after a recording started on
// keydown, blocking until it should stop: a press held longer than longPress
// records until release; a shorter tap switches to toggle mode, where the
// next tap stops. onToggle (optional) runs the moment toggle mode is entered
// (the app uses it to arm silence auto-close). Shared by the main record loop
// and `zee doctor`, so both behave identically.
//
// It returns how long keydown→keyup took as observed (downToUp) and whether it
// resolved to toggle mode — surfaced for diagnostics: a downToUp far above
// longPress on a quick tap means the keyup event arrived late (a stalled run
// loop), which is what makes a tap misfire as a hold.
func WaitStop(hk Hotkey, longPress time.Duration, onToggle func()) (downToUp time.Duration, toggled bool) {
	start := time.Now()
	timer := time.NewTimer(longPress)
	defer timer.Stop()
	select {
	case <-timer.C: // held: stop on release
		<-hk.Keyup()
		return time.Since(start), false
	case <-hk.Keyup(): // tap: toggle — stop on the next tap
		downToUp = time.Since(start)
		if onToggle != nil {
			onToggle()
		}
		<-hk.Keydown()
		<-hk.Keyup()
		return downToUp, true
	}
}

// Hotkey is a global push-to-talk hotkey. Rebind swaps the active combination
// at runtime (keeping the same Keydown/Keyup channels), and Capture records the
// next chord the user presses so the combination can be user-configured.
type Hotkey interface {
	Register() error
	Unregister()
	Keydown() <-chan struct{}
	Keyup() <-chan struct{}
	Rebind(c Combo) error
	Current() Combo
	// Capture records the next modifier+key chord the user presses and returns
	// it as a Combo. It returns ErrCaptureCanceled if the user presses Escape
	// (with no modifier) or cancel is closed, and on timeout.
	Capture(cancel <-chan struct{}) (Combo, error)
}

// Combo is a hotkey combination: one or more modifiers plus a key. Key is the
// platform-native key code (macOS virtual keycode / Linux evdev code), which is
// what the underlying registration API consumes directly. Label is a display
// string like "⌃⇧Space". Mods are canonical names: ctrl, shift, option, cmd.
type Combo struct {
	Mods  []string `json:"mods"`
	Key   int      `json:"key"`
	Label string   `json:"label"`
}

// IsZero reports whether c is unset (e.g. a config with no saved hotkey).
func (c Combo) IsZero() bool { return len(c.Mods) == 0 && c.Key == 0 }

// OrDefault is c, or the built-in default when nothing is saved — the one
// "which combo is in effect?" answer, shared by the app and the wizard.
func (c Combo) OrDefault() Combo {
	if c.IsZero() {
		return DefaultCombo()
	}
	return c
}

// Equal reports whether two combos bind the same chord. Modifier order is
// ignored (a hand-edited config may list mods in any order); Label is display
// only and not compared.
func (c Combo) Equal(o Combo) bool {
	if c.Key != o.Key || len(c.Mods) != len(o.Mods) {
		return false
	}
	a, b := slices.Clone(c.Mods), slices.Clone(o.Mods)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// modOrder is the canonical modifier order in a label, and modGlyphs their
// symbols. Both platforms build labels from these (and Display parses them
// back), so the rendering of a combo is defined exactly once.
var modOrder = []string{"ctrl", "option", "shift", "cmd"}

var modGlyphs = map[string]string{"ctrl": "⌃", "option": "⌥", "shift": "⇧", "cmd": "⌘"}

// modGlyphSet is modGlyphs' values as a string, for Display's rune scan.
const modGlyphSet = "⌃⌥⇧⌘"

// ComboLabel renders the compact label ("⌃⇧Space") for a set of modifier names
// and an already-rendered key glyph, in canonical order regardless of the order
// the modifiers arrive in.
func ComboLabel(mods []string, keyGlyph string) string {
	label := ""
	for _, m := range modOrder {
		if slices.Contains(mods, m) {
			label += modGlyphs[m]
		}
	}
	return label + keyGlyph
}

// Display renders the combo for humans as "⌃ + ⇧ + Space": the compact Label
// ("⌃⇧Space") is split into its modifier symbols and key, joined with " + ".
// Used everywhere a combo is shown (tray, setup/doctor prose).
func (c Combo) Display() string {
	var parts []string
	rest := c.Label
	for len(rest) > 0 {
		r, size := utf8.DecodeRuneInString(rest)
		if !strings.ContainsRune(modGlyphSet, r) {
			break
		}
		parts = append(parts, string(r))
		rest = rest[size:]
	}
	if rest != "" {
		parts = append(parts, rest)
	}
	return strings.Join(parts, " + ")
}

// ErrCaptureCanceled is returned by Capture when the user cancels (Escape /
// cancel signal) or no chord is recorded before the timeout.
var ErrCaptureCanceled = errors.New("hotkey capture canceled")

func hasModifier(mods []string) bool { return len(mods) > 0 }
