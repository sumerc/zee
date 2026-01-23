//go:build linux

package paste

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

func Send() error {
	if err := Init(); err != nil {
		return err
	}
	kb.SetKeys(keybd_event.VK_V)
	kb.HasCTRL(true)
	return kb.Launching()
}
