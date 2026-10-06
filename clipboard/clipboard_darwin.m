#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#include <string.h>
#include <unistd.h>

// NSPasteboard + CGEvent, called from clipboard_darwin.go. (Objective-C can't
// live in a cgo preamble — it is compiled as C — so it goes here, mirroring
// permissions/permissions_darwin.m.)
//
// Both replace things that cost real felt latency: pbcopy/pbpaste fork(), and
// fork freezes every thread for O(resident memory), which is significant once a
// local model is resident — while keybd_event held Cmd+V down for a hardcoded
// 100 ms sleep. Measurements in docs/design-notes.md.

// clipCopy replaces the pasteboard with one UTF-8 text item. Returns 1 on
// success. No locale involved, unlike the pbcopy child process it replaces.
int clipCopy(const char *utf8) {
	@autoreleasepool {
		NSString *s = [NSString stringWithUTF8String:utf8];
		if (s == nil) {
			return 0;
		}
		NSPasteboard *pb = [NSPasteboard generalPasteboard];
		[pb clearContents];
		return [pb setString:s forType:NSPasteboardTypeString] ? 1 : 0;
	}
}

// clipRead returns the pasteboard's text, malloc'd for the caller to free, or
// NULL when it holds no text (empty, or an image).
char *clipRead(void) {
	@autoreleasepool {
		NSString *s = [[NSPasteboard generalPasteboard] stringForType:NSPasteboardTypeString];
		if (s == nil) {
			return NULL;
		}
		return strdup([s UTF8String]);
	}
}

// clipPaste synthesizes Cmd+V into whichever app has focus. Deliberately the
// same event mechanism as the keybd_event call it replaces — NULL source,
// annotated session tap, flags set explicitly so a physically-held modifier
// cannot leak in. Requires Accessibility; without it macOS drops the events
// silently.
//
// The pause between down and up is load-bearing. Posted back to back, both
// events sit in a busy target's queue together; by the time it processes them
// V is already released, no Cmd+V key-equivalent fires, and the paste is lost
// silently — paste_key_ms stays ~1 ms because CGEventPost itself succeeds.
//
// Measured 2026-10-02 on an M5 Pro (15 cores), pasting tokens into a cmux pane
// running `cat` while 30 `yes` processes pinned every core (load avg 35→55),
// variants interleaved so each saw the same load:
//
//   gap     source / tap                          landed
//   0 ms    NULL / annotated session (as shipped)  3/15, 3/8, 6/8
//   5 ms    same                                   8/8
//   10 ms   same                                   8/8
//   30 ms   same                                   8/8
//   30 ms   HID system state / kCGHIDEventTap      8/8
//   30 ms   combined session / annotated session   8/8
//   0 ms    idle machine                           10/10
//
// Misses were drops, not delays: the pasteboard still held the token and it
// never arrived later. Tap and source made no difference; only the gap did.
// keybd_event used 100 ms; 5 ms held every time, 10 ms is margin for hosts
// slower than Ghostty (Electron, browsers).
void clipPaste(void) {
	const CGKeyCode kVK_V = 0x09;
	CGEventRef down = CGEventCreateKeyboardEvent(NULL, kVK_V, true);
	CGEventRef up = CGEventCreateKeyboardEvent(NULL, kVK_V, false);
	CGEventSetFlags(down, kCGEventFlagMaskCommand);
	CGEventSetFlags(up, kCGEventFlagMaskCommand);
	CGEventPost(kCGAnnotatedSessionEventTap, down);
	usleep(10000);
	CGEventPost(kCGAnnotatedSessionEventTap, up);
	CFRelease(down);
	CFRelease(up);
}
