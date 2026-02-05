//go:build !gui

package main

import "zee/audio"

// Stubs for non-GUI builds (these are never used since guiMode is false)
var guiAudioCtx audio.Context
var guiCaptureDevice audio.CaptureDevice

func initGUI() {
	panic("zee: built without GUI support (rebuild with -tags gui)")
}
