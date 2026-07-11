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

// DefaultCombo is the built-in hotkey (Ctrl+Shift+Space). KeySpace resolves to
// the correct platform keycode.
func DefaultCombo() Combo {
	return Combo{Mods: []string{"ctrl", "shift"}, Key: int(hotkey.KeySpace), Label: "⌃⇧Space"}
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
	go func() {
		for {
			select {
			case <-hk.Keydown():
				h.keydown <- struct{}{}
			case <-stop:
				return
			}
		}
	}()
	go func() {
		for {
			select {
			case <-hk.Keyup():
				h.keyup <- struct{}{}
			case <-stop:
				return
			}
		}
	}()
}

func (h *xHotkey) Rebind(c Combo) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !hasModifier(c.Mods) {
		return fmt.Errorf("hotkey needs at least one modifier")
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

var libMods = map[string]hotkey.Modifier{
	"ctrl":   hotkey.ModCtrl,
	"shift":  hotkey.ModShift,
	"option": hotkey.ModOption,
	"cmd":    hotkey.ModCmd,
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
