package hotkey

import (
	"errors"
	"time"
)

// captureTimeout bounds how long Capture waits for the user to press a chord.
const captureTimeout = 20 * time.Second

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

// ErrCaptureCanceled is returned by Capture when the user cancels (Escape /
// cancel signal) or no chord is recorded before the timeout.
var ErrCaptureCanceled = errors.New("hotkey capture canceled")

func hasModifier(mods []string) bool { return len(mods) > 0 }
