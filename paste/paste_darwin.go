//go:build darwin

package paste

import "github.com/micmonay/keybd_event"

func Send() error {
	kb, err := keybd_event.NewKeyBonding()
	if err != nil {
		return err
	}
	kb.SetKeys(keybd_event.VK_V)
	kb.HasSuper(true) // Cmd+V on macOS
	return kb.Launching()
}
