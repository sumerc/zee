#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#include <string.h>

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
// cannot leak in — minus the sleep between down and up. Requires Accessibility;
// without it macOS drops the events silently.
void clipPaste(void) {
	const CGKeyCode kVK_V = 0x09;
	CGEventRef down = CGEventCreateKeyboardEvent(NULL, kVK_V, true);
	CGEventRef up = CGEventCreateKeyboardEvent(NULL, kVK_V, false);
	CGEventSetFlags(down, kCGEventFlagMaskCommand);
	CGEventSetFlags(up, kCGEventFlagMaskCommand);
	CGEventPost(kCGAnnotatedSessionEventTap, down);
	CGEventPost(kCGAnnotatedSessionEventTap, up);
	CFRelease(down);
	CFRelease(up);
}
