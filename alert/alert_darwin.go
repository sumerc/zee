//go:build darwin

package alert

import (
	"os/exec"
	"strings"
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

func Confirm(msg, action string) bool {
	out, err := exec.Command("osascript", "-e",
		`display dialog "`+asQuote(msg)+`" with title "Zee" buttons {"Cancel", "`+asQuote(action)+`"} default button "`+asQuote(action)+`" with icon note`).Output()
	if err != nil {
		return false
	}
	return string(out) != ""
}

func show(msg, icon string) {
	exec.Command("osascript", "-e",
		`display dialog "`+asQuote(msg)+`" with title "Zee" buttons {"OK"} default button "OK" with icon `+icon).Run()
}

// asQuote escapes s for an AppleScript double-quoted literal. Raw quotes,
// backslashes, or newlines in the message (error strings often contain quoted
// URLs) are an osascript syntax error — the dialog silently never appears.
func asQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
