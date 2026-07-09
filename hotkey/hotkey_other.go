//go:build !linux

package hotkey

import (
	"fmt"
	"strings"

	"golang.design/x/hotkey"
)

// keyAlias maps user-facing key tokens to library key codes. Key names are
// stable across platforms in golang.design/x/hotkey (only the values differ),
// so this table is shared by darwin and windows; modAlias is per-OS.
var keyAlias = map[string]hotkey.Key{
	"space": hotkey.KeySpace, "spacebar": hotkey.KeySpace,
	"enter": hotkey.KeyReturn, "return": hotkey.KeyReturn,
	"tab": hotkey.KeyTab, "esc": hotkey.KeyEscape, "escape": hotkey.KeyEscape,
	"delete": hotkey.KeyDelete, "del": hotkey.KeyDelete,
	"up": hotkey.KeyUp, "down": hotkey.KeyDown, "left": hotkey.KeyLeft, "right": hotkey.KeyRight,
	"a": hotkey.KeyA, "b": hotkey.KeyB, "c": hotkey.KeyC, "d": hotkey.KeyD, "e": hotkey.KeyE,
	"f": hotkey.KeyF, "g": hotkey.KeyG, "h": hotkey.KeyH, "i": hotkey.KeyI, "j": hotkey.KeyJ,
	"k": hotkey.KeyK, "l": hotkey.KeyL, "m": hotkey.KeyM, "n": hotkey.KeyN, "o": hotkey.KeyO,
	"p": hotkey.KeyP, "q": hotkey.KeyQ, "r": hotkey.KeyR, "s": hotkey.KeyS, "t": hotkey.KeyT,
	"u": hotkey.KeyU, "v": hotkey.KeyV, "w": hotkey.KeyW, "x": hotkey.KeyX, "y": hotkey.KeyY,
	"z": hotkey.KeyZ,
	"0": hotkey.Key0, "1": hotkey.Key1, "2": hotkey.Key2, "3": hotkey.Key3, "4": hotkey.Key4,
	"5": hotkey.Key5, "6": hotkey.Key6, "7": hotkey.Key7, "8": hotkey.Key8, "9": hotkey.Key9,
	"f1": hotkey.KeyF1, "f2": hotkey.KeyF2, "f3": hotkey.KeyF3, "f4": hotkey.KeyF4,
	"f5": hotkey.KeyF5, "f6": hotkey.KeyF6, "f7": hotkey.KeyF7, "f8": hotkey.KeyF8,
	"f9": hotkey.KeyF9, "f10": hotkey.KeyF10, "f11": hotkey.KeyF11, "f12": hotkey.KeyF12,
}

// parseCombo turns a combo string like "ctrl+shift+space" into modifiers and a
// key. Tokens are case-insensitive and order-independent. Exactly one non-
// modifier key is required.
func parseCombo(combo string) ([]hotkey.Modifier, hotkey.Key, error) {
	if strings.TrimSpace(combo) == "" {
		combo = DefaultCombo
	}
	var mods []hotkey.Modifier
	var key hotkey.Key
	keySet := false
	seen := map[hotkey.Modifier]bool{}
	for _, raw := range strings.Split(combo, "+") {
		tok := strings.TrimSpace(strings.ToLower(raw))
		if tok == "" {
			continue
		}
		if m, ok := modAlias[tok]; ok {
			if !seen[m] {
				seen[m] = true
				mods = append(mods, m)
			}
			continue
		}
		if k, ok := keyAlias[tok]; ok {
			if keySet {
				return nil, 0, fmt.Errorf("hotkey %q has more than one non-modifier key", combo)
			}
			key = k
			keySet = true
			continue
		}
		return nil, 0, fmt.Errorf("hotkey %q has unknown token %q", combo, tok)
	}
	if !keySet {
		return nil, 0, fmt.Errorf("hotkey %q has no key (needs one non-modifier key)", combo)
	}
	return mods, key, nil
}

// Validate reports whether combo is a parseable hotkey.
func Validate(combo string) error {
	_, _, err := parseCombo(combo)
	return err
}

type xHotkey struct {
	hk      *hotkey.Hotkey
	keydown chan struct{}
	keyup   chan struct{}
}

// New builds a hotkey from a combo string (e.g. "ctrl+shift+space"). An empty
// or unparseable combo falls back to DefaultCombo.
func New(combo string) Hotkey {
	mods, key, err := parseCombo(combo)
	if err != nil {
		mods, key, _ = parseCombo(DefaultCombo)
	}
	return &xHotkey{
		hk:      hotkey.New(mods, key),
		keydown: make(chan struct{}, 1),
		keyup:   make(chan struct{}, 1),
	}
}

func (h *xHotkey) Register() error {
	if err := h.hk.Register(); err != nil {
		return err
	}
	go func() {
		for {
			<-h.hk.Keydown()
			h.keydown <- struct{}{}
		}
	}()
	go func() {
		for {
			<-h.hk.Keyup()
			h.keyup <- struct{}{}
		}
	}()
	return nil
}

func (h *xHotkey) Unregister() {
	h.hk.Unregister()
}

func (h *xHotkey) Keydown() <-chan struct{} {
	return h.keydown
}

func (h *xHotkey) Keyup() <-chan struct{} {
	return h.keyup
}

func Diagnose() (string, error) {
	return "hotkey support available", nil
}
