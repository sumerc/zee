#import <AVFoundation/AVFoundation.h>

// Feedback tones via AVAudioPlayer: the OS owns the audio machinery, so a beep
// is fire-and-forget with no app-managed device to init, keep warm, or
// serialize against capture. Players are built once from in-memory WAV bytes;
// each play restarts from the top so a rapid start-then-end beep both sound.

static AVAudioPlayer *_players[8];

void zeeBeepLoad(int idx, const void *wav, int len) {
	NSData *data = [NSData dataWithBytes:wav length:(NSUInteger)len];
	AVAudioPlayer *p = [[AVAudioPlayer alloc] initWithData:data error:nil];
	[p prepareToPlay];
	_players[idx] = p;
}

void zeeBeepPlay(int idx) {
	AVAudioPlayer *p = _players[idx];
	if (p == nil) return;
	p.currentTime = 0;
	[p play];
}
