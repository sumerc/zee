#import <Cocoa/Cocoa.h>

// Implemented in Go (capture_darwin.go).
extern void goHotkeyCaptured(int keycode, unsigned long mods);

static id _monitor = nil;

// startHotkeyCapture installs a passive global key-down monitor on the main
// run loop. Each key-down forwards its keycode + device-independent modifier
// flags to Go. It does not consume the event.
void startHotkeyCapture(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (_monitor != nil) {
			return;
		}
		_monitor = [NSEvent addGlobalMonitorForEventsMatchingMask:NSEventMaskKeyDown
			handler:^(NSEvent *e) {
				unsigned long flags = (unsigned long)([e modifierFlags] &
					NSEventModifierFlagDeviceIndependentFlagsMask);
				goHotkeyCaptured((int)[e keyCode], flags);
			}];
	});
}

void stopHotkeyCapture(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (_monitor != nil) {
			[NSEvent removeMonitor:_monitor];
			_monitor = nil;
		}
	});
}
