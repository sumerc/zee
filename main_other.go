//go:build !linux

package main

import (
	"os"
	"runtime"

	"golang.design/x/hotkey/mainthread"
)

func init() {
	runtime.LockOSThread()
}

func main() {
	// Set up crash logging early, before any CGO code runs
	initCrashLog()

	// Check for -gui flag early (before flag.Parse in run())
	for _, arg := range os.Args[1:] {
		if arg == "-gui" {
			initGUI() // takes main thread, calls run() in goroutine
			return
		}
	}
	mainthread.Init(run)
}
