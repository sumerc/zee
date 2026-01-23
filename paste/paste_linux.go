//go:build linux

package paste

import "github.com/micmonay/keybd_event"

func Send() error {
	kb, err := keybd_event.NewKeyBonding()
	if err != nil {
		return err
	}
	kb.SetKeys(keybd_event.VK_V)
	kb.HasCTRL(true) // Ctrl+V on Linux
	return kb.Launching()
}
