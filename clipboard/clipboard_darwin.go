package clipboard

/*
#cgo LDFLAGS: -framework ApplicationServices
#include <ApplicationServices/ApplicationServices.h>

static int testAccessibility() {
	return AXIsProcessTrusted();
}
*/
import "C"

import (
	"os"
	"strings"
	"sync"

	"github.com/micmonay/keybd_event"
)

var (
	kb     keybd_event.KeyBonding
	kbOnce sync.Once
	kbErr  error
)

// ensureUTF8Locale guarantees pbcopy/pbpaste interpret text as UTF-8.
// GUI apps launched from Finder inherit no LANG/LC_CTYPE, so pbcopy falls
// back to a legacy encoding and mangles multi-byte characters (e.g. Turkish
// ğ ş ı İ ç ö ü). Setting LC_CTYPE to a UTF-8 locale fixes this for the child
// process that atotto/clipboard exec's. We only override when the current
// ctype locale is not already UTF-8, so an explicit tr_TR.UTF-8 etc. is kept.
func init() {
	isUTF8 := func(v string) bool {
		v = strings.ToLower(v)
		return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
	}
	if isUTF8(os.Getenv("LC_ALL")) || isUTF8(os.Getenv("LC_CTYPE")) || isUTF8(os.Getenv("LANG")) {
		return
	}
	os.Setenv("LC_CTYPE", "en_US.UTF-8")
}

func Init() error {
	kbOnce.Do(func() {
		kb, kbErr = keybd_event.NewKeyBonding()
	})
	return kbErr
}

func Paste() error {
	if disabled {
		return nil
	}
	if err := Init(); err != nil {
		return err
	}
	kb.SetKeys(keybd_event.VK_V)
	kb.HasSuper(true) // Cmd+V on macOS
	return kb.Launching()
}

func CheckAccessibility() bool {
	return C.testAccessibility() == 1
}
