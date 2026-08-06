package clipboard

/*
#cgo LDFLAGS: -framework AppKit -framework ApplicationServices
#include <stdlib.h>
#include <ApplicationServices/ApplicationServices.h>

int clipCopy(const char *utf8);
char *clipRead(void);
void clipPaste(void);

static int testAccessibility() {
	return AXIsProcessTrusted();
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// Init is a no-op on macOS — NSPasteboard and CGEvent need no setup. It exists
// because the Linux backend must open /dev/uinput before the first paste.
func Init() error { return nil }

func write(text string) error {
	c := C.CString(text)
	defer C.free(unsafe.Pointer(c))
	if C.clipCopy(c) == 0 {
		return errors.New("pasteboard rejected the write")
	}
	return nil
}

func read() (string, error) {
	c := C.clipRead()
	if c == nil {
		return "", nil // no text on the pasteboard; not an error
	}
	defer C.free(unsafe.Pointer(c))
	return C.GoString(c), nil
}

// Paste fires Cmd+V. macOS reports nothing when Accessibility is missing — the
// events are simply dropped — so there is no error to return here; the setup
// wizard uses CheckAccessibility to catch that case up front.
func Paste() error {
	C.clipPaste()
	return nil
}

func CheckAccessibility() bool {
	return C.testAccessibility() == 1
}
