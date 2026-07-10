//go:build windows

package audio

// No audio playback on Windows — beeps are no-ops. (Capture still works via
// the malgo backend in capture_other.go.)

func initSound()    {}
func playOne(sound) {}
