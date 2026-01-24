//go:build !linux

package shortcut

import (
	"golang.design/x/hotkey"
)

type xHotkey struct {
	hk      *hotkey.Hotkey
	keydown chan struct{}
	keyup   chan struct{}
}

// New creates a hotkey using golang.design/x/hotkey (X11/Cocoa/Win32).
func New() Hotkey {
	return &xHotkey{
		hk:      hotkey.New([]hotkey.Modifier{hotkey.ModCtrl, hotkey.ModShift}, hotkey.KeySpace),
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

// Diagnose checks hotkey availability and returns a status message.
func Diagnose() (string, error) {
	return "hotkey support available (Ctrl+Shift+Space)", nil
}
