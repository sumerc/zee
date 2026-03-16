//go:build darwin

package alert

import "os/exec"

func Error(msg string) {
	show(msg, "stop")
}

func Warn(msg string) {
	show(msg, "caution")
}

func show(msg, icon string) {
	exec.Command("osascript", "-e",
		`display dialog "`+msg+`" with title "Zee" buttons {"OK"} default button "OK" with icon `+icon).Run()
}
