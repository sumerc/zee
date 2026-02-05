//go:build gui

package main

import (
	"fmt"
	"os"
	"runtime"

	"zee/audio"
	"zee/encoder"
	"zee/gui"
)

var guiApp *gui.App

// Audio context initialized on main thread for macOS Core Audio compatibility
var guiAudioCtx audio.Context
var guiCaptureDevice audio.CaptureDevice

func initGUI() {
	guiMode = true

	// Verify we're on the main thread (should be locked from init())
	fmt.Fprintf(os.Stderr, "[audio] Initializing on goroutine, LockOSThread active\n")

	// Initialize audio context on main thread BEFORE Fyne starts.
	// macOS Core Audio requires main thread access for proper capture.
	var err error
	guiAudioCtx, err = audio.NewContext()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing audio context: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "[audio] Context created successfully\n")

	captureConfig := audio.CaptureConfig{
		SampleRate: encoder.SampleRate,
		Channels:   encoder.Channels,
	}
	guiCaptureDevice, err = guiAudioCtx.NewCapture(nil, captureConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing capture device: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "[audio] Capture device created successfully\n")

	// Test start/stop to verify device works
	if err := guiCaptureDevice.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "[audio] WARNING: Test start failed: %v\n", err)
	} else {
		guiCaptureDevice.Stop()
		fmt.Fprintf(os.Stderr, "[audio] Test start/stop successful\n")
	}

	// Lock this goroutine to OS thread for Fyne/GLFW
	runtime.LockOSThread()

	guiApp = gui.NewApp(func() {
		run()
	})
	sink = guiApp
	if err := gui.Run(guiApp); err != nil {
		guiCaptureDevice.Close()
		guiAudioCtx.Close()
		panic(err)
	}
}
