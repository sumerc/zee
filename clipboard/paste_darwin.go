//go:build darwin

package clipboard

import (
	"sync"

	"github.com/micmonay/keybd_event"
)

var (
	kb     keybd_event.KeyBonding
	kbOnce sync.Once
	kbErr  error
)

func Init() error {
	kbOnce.Do(func() {
		kb, kbErr = keybd_event.NewKeyBonding()
	})
	return kbErr
}

func Paste() error {
	if err := Init(); err != nil {
		return err
	}
	kb.SetKeys(keybd_event.VK_V)
	kb.HasSuper(true) // Cmd+V on macOS
	return kb.Launching()
}

// Verify checks that the keyboard event binding is initialized.
func Verify() (string, error) {
	if err := Init(); err != nil {
		return "", err
	}
	return "keyboard event binding OK (Cmd+V)", nil
}
