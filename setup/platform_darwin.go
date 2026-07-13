//go:build darwin

package setup

/*
#include <unistd.h>
*/
import "C"

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

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
	// A running tray instance must die BEFORE the `open`: LaunchServices would
	// otherwise just activate it (ignoring --args) and -W would block on the
	// tray instead of the wizard.
	quitRunningApp()
	cmd := exec.Command("open", "-W", "-a", app,
		"--stdin", tty, "--stdout", tty, "--stderr", tty,
		"--args", "-setup")
	cmd.Stderr = os.Stderr // surface `open` errors; the child writes to the tty directly
	if err := cmd.Run(); err != nil {
		return 0, false // open failed — fall back to running in place
	}
	return 0, true
}

// launchInstalledApp starts the tray app after setup completes (installed
// bundle only; reports whether it launched). -n forces a new instance:
// without it, LaunchServices sees the still-running wizard (same bundle) and
// merely activates it, so no tray would ever start.
func launchInstalledApp() bool {
	app := appBundlePath()
	if app == "" {
		return false
	}
	return exec.Command("open", "-n", "-a", app).Start() == nil
}

// quitRunningApp terminates other running zee processes (tray instances) so
// they don't hold the global hotkey during the wizard's tests and can't be
// mistaken for the fresh post-setup launch. It kills by pid rather than an
// AppleScript "tell application Zee to quit": once the wizard itself runs as a
// second Zee.app instance the AppleEvent target is ambiguous (it can hit the
// wizard, which has no AppleEvent handler). SIGTERM is graceful — the app's
// signal handler runs its normal shutdown path.
func quitRunningApp() {
	out, err := exec.Command("pgrep", "-x", "zee").Output()
	if err != nil {
		return // no zee processes
	}
	var victims []int
	self := os.Getpid()
	for _, f := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(f); err == nil && pid != self {
			syscall.Kill(pid, syscall.SIGTERM)
			victims = append(victims, pid)
		}
	}
	// Wait briefly so the global hotkey is actually released before our tests.
	for i := 0; i < 20 && anyAlive(victims); i++ {
		time.Sleep(100 * time.Millisecond)
	}
}

func anyAlive(pids []int) bool {
	for _, p := range pids {
		if syscall.Kill(p, 0) == nil {
			return true
		}
	}
	return false
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
