//go:build linux

package main

import "os"

func main() {
	// Set up crash logging early, before any CGO code runs
	initCrashLog()

	for _, arg := range os.Args[1:] {
		if arg == "-gui" {
			initGUI()
			return
		}
	}
	run()
}
