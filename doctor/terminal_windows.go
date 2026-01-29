//go:build windows

package doctor

import (
	"os"
	"os/signal"
)

func resetTerminal() {
	// Not needed on Windows
}

func setupInterruptHandler() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	go func() {
		<-sigChan
		println("\nInterrupted")
		os.Exit(1)
	}()
}
