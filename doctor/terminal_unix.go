//go:build !windows

package doctor

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func resetTerminal() {
	exec.Command("stty", "sane").Run()
}

func setupInterruptHandler() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		println("\nInterrupted")
		os.Exit(1)
	}()
}
