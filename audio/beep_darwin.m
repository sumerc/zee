#import <AVFoundation/AVFoundation.h>

// Feedback tones via AVAudioPlayer: the OS owns the audio machinery, so a beep
// needs no app-managed device to init, keep warm, or serialize against capture.
// Players are built once from in-memory WAV bytes; each play restarts from the
// top so a rapid start-then-end beep both sound.

static AVAudioPlayer *_players[8];

// Serial, so two beeps never touch one player's currentTime concurrently —
// AVAudioPlayer is not documented thread-safe.
static dispatch_queue_t beepQueue(void) {
	static dispatch_queue_t q;
	static dispatch_once_t once;
	dispatch_once(&once, ^{
		q = dispatch_queue_create("dev.zee.beep", DISPATCH_QUEUE_SERIAL);
	});
	return q;
}

void zeeBeepLoad(int idx, const void *wav, int len) {
	NSData *data = [NSData dataWithBytes:wav length:(NSUInteger)len];
	AVAudioPlayer *p = [[AVAudioPlayer alloc] initWithData:data error:nil];
	[p prepareToPlay];
	_players[idx] = p;
}

// zeeBeepPlay dispatches instead of playing inline: -play blocks while it starts
// the audio hardware — prepareToPlay does not prevent it, and the hardware powers
// back down between beeps — and that landed on the push-to-talk path twice per
// dictation, at press and at release. The user hears the tone at the same moment;
// only the caller's wait is gone. Measured cost in docs/design-notes.md.
void zeeBeepPlay(int idx) {
	AVAudioPlayer *p = _players[idx];
	if (p == nil) return;
	dispatch_async(beepQueue(), ^{
		p.currentTime = 0;
		[p play];
	});
}
