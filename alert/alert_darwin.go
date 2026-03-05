//go:build darwin

package alert

import "os/exec"

func Show(msg string) {
	exec.Command("osascript", "-e",
		`display dialog "`+msg+`" with title "Zee" buttons {"OK"} default button "OK" with icon stop`).Run()
}
