//go:build darwin

package overlay

/*
#cgo darwin LDFLAGS: -framework Cocoa -framework QuartzCore
#include <stdlib.h>
void zeeOverlayShow(void);
void zeeOverlayHide(void);
void zeeOverlaySetState(const char *label, double r, double g, double b, int showMeter);
void zeeOverlayPushLevel(float v);
void zeeOverlayRun(void);
*/
import "C"

import "unsafe"

func show() { C.zeeOverlayShow() }
func hide() { C.zeeOverlayHide() }
func run()  { C.zeeOverlayRun() }

func setState(label string, r, g, b float64, showMeter bool) {
	c := C.CString(label)
	defer C.free(unsafe.Pointer(c))
	var meter C.int
	if showMeter {
		meter = 1
	}
	C.zeeOverlaySetState(c, C.double(r), C.double(g), C.double(b), meter)
}

func pushLevel(v float32) { C.zeeOverlayPushLevel(C.float(v)) }
