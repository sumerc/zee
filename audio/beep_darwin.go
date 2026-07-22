//go:build darwin

package audio

/*
#cgo darwin LDFLAGS: -framework AVFoundation -framework Foundation
void zeeBeepLoad(int idx, const void *wav, int len);
void zeeBeepPlay(int idx);
*/
import "C"

import (
	"encoding/binary"
	"unsafe"
)

// Feedback tones play via AVAudioPlayer (see beep_darwin.m): the OS owns the
// audio machinery, so each beep is fire-and-forget with no playback device to
// init, keep warm, or serialize against capture (deviceMu guards capture
// only). App-managed malgo playback was tried both ways and lost: reinit-per-
// tone stalled the run loop 100–600ms (audibly late end-beep, delayed hotkey
// events), a kept-warm device intermittently went silent. Tones are still
// synthesized by buildSamples (the single source of truth) and handed to the
// OS as in-memory WAV bytes at startup.
func initSound() {
	buildSamples()
	for s := startSound; s < numSounds; s++ {
		wav := pcmWAV(pcm16LE(samples[s]), beepSampleRate)
		C.zeeBeepLoad(C.int(s), unsafe.Pointer(&wav[0]), C.int(len(wav)))
	}
}

func playOne(s sound) { C.zeeBeepPlay(C.int(s)) }

// pcm16LE flattens canonical int16 samples to little-endian bytes.
func pcm16LE(s []int16) []byte {
	b := make([]byte, 2*len(s))
	for i, v := range s {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(v))
	}
	return b
}
