//go:build windows

package beep

// No audio playback on Windows - beeps disabled.

func Init()      {}
func PlayStart() {}
func PlayEnd()   {}
