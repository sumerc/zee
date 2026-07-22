//go:build darwin

package audio

/*
#cgo LDFLAGS: -framework AudioToolbox -framework CoreFoundation
#include <stdlib.h>
#include <string.h>
#include <AudioToolbox/AudioToolbox.h>

static SystemSoundID zee_sound_load(const char* path) {
	CFURLRef url = CFURLCreateFromFileSystemRepresentation(NULL, (const UInt8*)path, (CFIndex)strlen(path), false);
	if (url == NULL) return 0;
	SystemSoundID sid = 0;
	OSStatus st = AudioServicesCreateSystemSoundID(url, &sid);
	CFRelease(url);
	return st == kAudioServicesNoError ? sid : 0;
}

static void zee_sound_play(SystemSoundID sid) { AudioServicesPlaySystemSound(sid); }
*/
import "C"

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"unsafe"

	"zee/log"
)

// Feedback tones play via AudioToolbox System Sound Services: the OS sound
// server owns the audio machinery, so each beep is one fire-and-forget C call
// with no playback device to init, keep warm, or serialize against capture
// (deviceMu guards capture only). App-managed malgo playback was tried twice
// and lost both ways: reinit-per-tone stalls the run loop 100–600ms (audibly
// late end-beep, delayed hotkey events), a kept-warm device intermittently
// went silent. Tones are still synthesized by buildSamples (the single source
// of truth) and serialized once at startup to small WAVs in the cache dir,
// because AudioServicesCreateSystemSoundID takes file URLs only. They play on
// the user's sound-effects output at alert volume, like every UI feedback
// sound.

var soundIDs [numSounds]C.SystemSoundID

var soundNames = [numSounds]string{"start", "end", "error", "denied"}

func initSound() {
	buildSamples()

	dir, err := os.UserCacheDir()
	if err != nil {
		log.Warnf("beep: no cache dir: %v", err)
		return
	}
	dir = filepath.Join(dir, "zee")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Warnf("beep: %v", err)
		return
	}

	for s := startSound; s < numSounds; s++ {
		pcm := make([]byte, 2*len(samples[s]))
		for i, v := range samples[s] {
			binary.LittleEndian.PutUint16(pcm[i*2:], uint16(v))
		}
		path := filepath.Join(dir, "tone-"+soundNames[s]+".wav")
		if err := os.WriteFile(path, pcmWAV(pcm, beepSampleRate), 0o644); err != nil {
			log.Warnf("beep: write %s: %v", path, err)
			continue
		}
		cPath := C.CString(path)
		soundIDs[s] = C.zee_sound_load(cPath)
		C.free(unsafe.Pointer(cPath))
		if soundIDs[s] == 0 {
			log.Warnf("beep: load %s failed", path)
		}
	}
}

func playOne(s sound) {
	if id := soundIDs[s]; id != 0 {
		C.zee_sound_play(id)
	}
}
