//go:build windows

package audio

// No audio playback on Windows — beeps are no-ops. (Capture still works via
// the malgo backend in capture_windows.go.)

func initSound()    {}
func playOne(sound) {}
