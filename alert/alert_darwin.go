//go:build darwin

package alert

import (
	"os/exec"
)

func Error(msg string) {
	show(msg, "stop")
}

func Warn(msg string) {
	show(msg, "caution")
}

func Info(msg string) {
	show(msg, "note")
}

// Untrusted text (msg, action) is passed via argv rather than interpolated into
// the AppleScript source, so quotes/backslashes/newlines/Unicode need no escaping.
// icon is a fixed AppleScript keyword (note/caution/stop) we control, so it stays
// in the source.

func Confirm(msg, action string) bool {
	if underTest {
		return false
	}
	const script = `on run argv
		display dialog (item 1 of argv) with title "Zee" buttons {"Cancel", item 2 of argv} default button (item 2 of argv) with icon note
	end run`
	out, err := exec.Command("osascript", "-e", script, msg, action).Output()
	if err != nil {
		return false
	}
	return string(out) != ""
}

func show(msg, icon string) {
	if underTest {
		return
	}
	script := `on run argv
		display dialog (item 1 of argv) with title "Zee" buttons {"OK"} default button "OK" with icon ` + icon + `
	end run`
	exec.Command("osascript", "-e", script, msg).Run()
}
