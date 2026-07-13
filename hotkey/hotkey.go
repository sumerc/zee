package hotkey

import (
	"errors"
	"slices"
	"time"
)

// captureTimeout bounds how long Capture waits for the user to press a chord.
const captureTimeout = 20 * time.Second

// DefaultLongPress separates the two press styles: held longer = push-to-talk
// (release stops), shorter = toggle (next tap stops).
const DefaultLongPress = 350 * time.Millisecond

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
func WaitStop(hk Hotkey, longPress time.Duration, onToggle func()) {
	timer := time.NewTimer(longPress)
	defer timer.Stop()
	select {
	case <-timer.C: // held: stop on release
		<-hk.Keyup()
	case <-hk.Keyup(): // tap: toggle — stop on the next tap
		if onToggle != nil {
			onToggle()
		}
		<-hk.Keydown()
		<-hk.Keyup()
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

// ErrCaptureCanceled is returned by Capture when the user cancels (Escape /
// cancel signal) or no chord is recorded before the timeout.
var ErrCaptureCanceled = errors.New("hotkey capture canceled")

func hasModifier(mods []string) bool { return len(mods) > 0 }
