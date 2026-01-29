//go:build windows

package shutdown

import (
	"os"
	"os/signal"
)

func Notify(ch chan os.Signal) {
	signal.Notify(ch, os.Interrupt)
}
