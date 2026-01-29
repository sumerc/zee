//go:build !windows

package shutdown

import (
	"os"
	"os/signal"
	"syscall"
)

func Notify(ch chan os.Signal) {
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
}
