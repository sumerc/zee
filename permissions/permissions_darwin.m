#import <AVFoundation/AVFoundation.h>
#import <ApplicationServices/ApplicationServices.h>

// C wrappers over the Objective-C / TCC APIs, called from permissions_darwin.go.
// (Objective-C can't live in a cgo preamble — it's compiled as C — so it goes
// here, mirroring hotkey/capture_darwin.m.)

// AVAuthorizationStatus for audio: 0 notDetermined, 1 restricted, 2 denied, 3 authorized.
int permMicStatus(void) {
	return (int)[AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeAudio];
}

// permMicRequest shows the microphone prompt when the status is notDetermined and
// blocks until the user answers, returning 1 granted / 0 denied. When the status
// is already decided the handler fires immediately with that answer.
int permMicRequest(void) {
	dispatch_semaphore_t sem = dispatch_semaphore_create(0);
	__block BOOL ok = NO;
	[AVCaptureDevice requestAccessForMediaType:AVMediaTypeAudio
		completionHandler:^(BOOL granted) {
			ok = granted;
			dispatch_semaphore_signal(sem);
		}];
	dispatch_semaphore_wait(sem, DISPATCH_TIME_FOREVER);
	return ok ? 1 : 0;
}

int permAXTrusted(void) { return AXIsProcessTrusted() ? 1 : 0; }

// permAXPrompt returns the current trust state and, when untrusted, asks macOS to
// show the "open System Settings" prompt. The grant is asynchronous (the user
// flips a toggle), so callers poll permAXTrusted afterwards.
int permAXPrompt(void) {
	const void *keys[] = { kAXTrustedCheckOptionPrompt };
	const void *vals[] = { kCFBooleanTrue };
	CFDictionaryRef opts = CFDictionaryCreate(kCFAllocatorDefault, keys, vals, 1,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	int trusted = AXIsProcessTrustedWithOptions(opts) ? 1 : 0;
	CFRelease(opts);
	return trusted;
}
