//go:build !linux

package hotkey

import (
	"fmt"
	"sync"

	"golang.design/x/hotkey"
)

type xHotkey struct {
	mu      sync.Mutex
	hk      *hotkey.Hotkey
	combo   Combo
	stop    chan struct{} // stops the current forwarder goroutines
	keydown chan struct{}
	keyup   chan struct{}
}

// DefaultCombo is the built-in hotkey (Option+Space). KeySpace resolves to the
// correct platform keycode.
func DefaultCombo() Combo {
	return Combo{Mods: []string{"option"}, Key: int(hotkey.KeySpace), Label: "⌥Space"}
}

func New(c Combo) Hotkey {
	if c.IsZero() {
		c = DefaultCombo()
	}
	mods, key := toLib(c)
	return &xHotkey{
		hk:      hotkey.New(mods, key),
		combo:   c,
		keydown: make(chan struct{}, 1),
		keyup:   make(chan struct{}, 1),
	}
}

func (h *xHotkey) Register() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Validate before registering: toLib silently drops unknown modifier names,
	// so a typo like "alt" (for "option") would otherwise register the bare key
	// (e.g. Space) system-wide. New() built h.hk best-effort; catch it here.
	if err := validateCombo(h.combo); err != nil {
		return err
	}
	if err := h.hk.Register(); err != nil {
		return err
	}
	h.stop = make(chan struct{})
	h.forward(h.hk, h.stop)
	return nil
}

// forward pumps the underlying hotkey's events into our stable channels until
// stop is closed (on Rebind/Unregister), so the app's listener never restarts.
func (h *xHotkey) forward(hk *hotkey.Hotkey, stop chan struct{}) {
	// The sends select on stop too: a forwarder parked on a full channel would
	// otherwise outlive close(stop) and deliver its stale event into the shared
	// channel after a Rebind — one phantom keydown from the *old* combo.
	go func() {
		for {
			select {
			case <-hk.Keydown():
				select {
				case h.keydown <- struct{}{}:
				case <-stop:
					return
				}
			case <-stop:
				return
			}
		}
	}()
	go func() {
		for {
			select {
			case <-hk.Keyup():
				select {
				case h.keyup <- struct{}{}:
				case <-stop:
					return
				}
			case <-stop:
				return
			}
		}
	}()
}

func (h *xHotkey) Rebind(c Combo) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := validateCombo(c); err != nil {
		return err
	}
	mods, key := toLib(c)
	// Register the new combo BEFORE tearing down the old one, so a rejected
	// combo (already owned by the system or another app) leaves the current
	// hotkey fully intact.
	newHk := hotkey.New(mods, key)
	if err := newHk.Register(); err != nil {
		return fmt.Errorf("register %s: %w", c.Label, err)
	}
	if h.stop != nil {
		close(h.stop)
	}
	h.hk.Unregister()
	h.hk = newHk
	h.stop = make(chan struct{})
	h.forward(newHk, h.stop)
	h.combo = c
	return nil
}

func (h *xHotkey) Current() Combo {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.combo
}

func (h *xHotkey) Capture(cancel <-chan struct{}) (Combo, error) {
	return captureChord(cancel)
}

func (h *xHotkey) Unregister() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stop != nil {
		close(h.stop)
		h.stop = nil
	}
	h.hk.Unregister()
}

func (h *xHotkey) Keydown() <-chan struct{} { return h.keydown }
func (h *xHotkey) Keyup() <-chan struct{}   { return h.keyup }

// libMods maps our platform-neutral modifier names to the backend's constants.
// The set is identical across darwin/windows but the constants differ (option→
// Option vs Alt, cmd→Cmd vs Win), so each platform supplies its own in
// mods_<goos>.go — keeping this file free of any OS-only symbol.

// validateCombo rejects a combo that can't bind safely: no modifier (would
// grab a bare key system-wide), an unrecognized modifier name (dropped by
// toLib, same bare-key hazard), or a key code toLib's uint8 cast would
// silently truncate to a different key. Keep in sync with libMods.
func validateCombo(c Combo) error {
	if !hasModifier(c.Mods) {
		return fmt.Errorf("hotkey needs at least one modifier")
	}
	for _, m := range c.Mods {
		if _, ok := libMods[m]; !ok {
			return fmt.Errorf("unknown modifier %q (use ctrl, shift, option, cmd)", m)
		}
	}
	if c.Key < 0 || c.Key > 255 {
		return fmt.Errorf("hotkey key code %d out of range (0-255)", c.Key)
	}
	return nil
}

func toLib(c Combo) ([]hotkey.Modifier, hotkey.Key) {
	mods := make([]hotkey.Modifier, 0, len(c.Mods))
	for _, m := range c.Mods {
		if lm, ok := libMods[m]; ok {
			mods = append(mods, lm)
		}
	}
	return mods, hotkey.Key(uint8(c.Key))
}

func Diagnose() (string, error) {
	return "hotkey support available (" + DefaultCombo().Label + " default)", nil
}
