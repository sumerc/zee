//go:build darwin

package hotkey

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation
#include <ApplicationServices/ApplicationServices.h>
#include <CoreFoundation/CoreFoundation.h>
#include <pthread.h>

extern void goHotkeyCallback(int isDown);

static CFMachPortRef tapRef = NULL;
static CFRunLoopSourceRef sourceRef = NULL;
static CFRunLoopRef loopRef = NULL;
static int prevFnDown = 0;

static CGEventRef eventCallback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *refcon) {
	if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
		CGEventTapEnable(tapRef, true);
		return event;
	}
	if (type == kCGEventFlagsChanged) {
		int fnDown = (CGEventGetFlags(event) & NX_SECONDARYFNMASK) != 0;
		if (fnDown && !prevFnDown) {
			goHotkeyCallback(1);
			prevFnDown = 1;
			return NULL;
		} else if (!fnDown && prevFnDown) {
			goHotkeyCallback(0);
			prevFnDown = 0;
			return NULL;
		}
	}
	return event;
}

static void* runTapThread(void* arg) {
	loopRef = CFRunLoopGetCurrent();
	CFRunLoopAddSource(loopRef, sourceRef, kCFRunLoopCommonModes);
	CGEventTapEnable(tapRef, true);
	CFRunLoopRun();
	return NULL;
}

static int createTap() {
	tapRef = CGEventTapCreate(
		kCGSessionEventTap,
		kCGHeadInsertEventTap,
		kCGEventTapOptionDefault,
		CGEventMaskBit(kCGEventFlagsChanged),
		eventCallback, NULL
	);
	if (tapRef == NULL) return -1;

	sourceRef = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, tapRef, 0);
	if (sourceRef == NULL) {
		CFRelease(tapRef);
		tapRef = NULL;
		return -1;
	}

	pthread_t thread;
	pthread_create(&thread, NULL, runTapThread, NULL);
	pthread_detach(thread);
	return 0;
}

static void destroyTap() {
	if (loopRef) {
		CFRunLoopStop(loopRef);
		loopRef = NULL;
	}
	if (sourceRef) {
		CFRelease(sourceRef);
		sourceRef = NULL;
	}
	if (tapRef) {
		CFRelease(tapRef);
		tapRef = NULL;
	}
}
*/
import "C"

import "fmt"

var (
	globalKeydown chan struct{}
	globalKeyup   chan struct{}
)

//export goHotkeyCallback
func goHotkeyCallback(isDown C.int) {
	if isDown == 1 {
		select {
		case globalKeydown <- struct{}{}:
		default:
		}
	} else {
		select {
		case globalKeyup <- struct{}{}:
		default:
		}
	}
}

type darwinHotkey struct {
	keydown chan struct{}
	keyup   chan struct{}
}

func New() Hotkey {
	h := &darwinHotkey{
		keydown: make(chan struct{}, 1),
		keyup:   make(chan struct{}, 1),
	}
	globalKeydown = h.keydown
	globalKeyup = h.keyup
	return h
}

func (h *darwinHotkey) Register() error {
	if C.createTap() != 0 {
		return fmt.Errorf("accessibility permission required")
	}
	return nil
}

func (h *darwinHotkey) Unregister() {
	C.destroyTap()
}

func (h *darwinHotkey) Keydown() <-chan struct{} { return h.keydown }
func (h *darwinHotkey) Keyup() <-chan struct{}   { return h.keyup }

func Diagnose() (string, error) {
	return "hotkey support available (Fn/Globe key)", nil
}
