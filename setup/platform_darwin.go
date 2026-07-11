//go:build darwin

package setup

/*
#include <unistd.h>
*/
import "C"

import (
	"os"
	"os/exec"
	"strings"

	"zee/config"
)

// maybeReexec relaunches the wizard through `open` when it was started from a
// terminal as the installed app bundle, so the process is parented by launchd
// and macOS attributes Microphone/Accessibility prompts to Zee.app instead of
// the terminal. The current tty is wired as the child's stdio so the wizard
// stays interactive. Returns done=true when it re-exec'd (caller should exit).
//
// It is a no-op — done=false — for dev builds (TCC correctly attributes to the
// terminal there) and when already launchd-parented (launched via open, e.g. by
// install.sh), detected via getppid()==1.
func maybeReexec() (code int, done bool) {
	if !config.IsAppBundle() || os.Getppid() == 1 {
		return 0, false
	}
	tty := ttyName()
	app := appBundlePath()
	if tty == "" || app == "" {
		return 0, false // no controlling tty or not a resolvable bundle: run in place
	}
	cmd := exec.Command("open", "-W", "-a", app,
		"--stdin", tty, "--stdout", tty, "--stderr", tty,
		"--args", "-setup")
	cmd.Stderr = os.Stderr // surface `open` errors; the child writes to the tty directly
	if err := cmd.Run(); err != nil {
		return 0, false // open failed — fall back to running in place
	}
	return 0, true
}

// launchInstalledApp starts the tray app after setup completes (installed bundle
// only). open -a with no args launches or activates the normal (no -setup) app.
func launchInstalledApp() {
	if app := appBundlePath(); app != "" {
		exec.Command("open", "-a", app).Start()
	}
}

// quitRunningApp best-effort quits an already-running Zee tray instance so it
// doesn't hold the global hotkey while the wizard tests registration, and so the
// post-setup launch starts fresh.
func quitRunningApp() {
	exec.Command("osascript", "-e", `tell application "Zee" to quit`).Run()
}

func ttyName() string {
	if p := C.ttyname(C.int(0)); p != nil { // STDIN_FILENO
		return C.GoString(p)
	}
	return ""
}

// appBundlePath returns the <...>.app path containing this executable, or "".
func appBundlePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	const marker = ".app/Contents/MacOS/"
	if i := strings.Index(exe, marker); i >= 0 {
		return exe[:i+len(".app")]
	}
	return ""
}
