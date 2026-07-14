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

	"zee/config"
)

// maybeReexec relaunches this mode (the `setup` or `doctor` subcommand)
// through `open` when it was started from a terminal as the installed app
// bundle, so the process is parented by launchd and macOS attributes
// Microphone/Accessibility prompts to Zee.app instead of the terminal. The
// current tty is wired as the child's stdio so it stays interactive. Returns
// done=true when it re-exec'd (caller should exit).
//
// It is a no-op — done=false — for dev builds (TCC correctly attributes to the
// terminal there) and when already launchd-parented (launched via open, e.g. by
// install.sh), detected via getppid()==1.
func maybeReexec(mode string) (code int, done bool) {
	if !config.IsAppBundle() || os.Getppid() == 1 {
		return 0, false
	}
	tty := ttyName()
	app := appBundlePath()
	if tty == "" || app == "" {
		return 0, false // no controlling tty or not a resolvable bundle: run in place
	}
	// -n is load-bearing: exec'ing the bundle binary directly (this process)
	// already registers as the running Zee.app instance with LaunchServices,
	// so a plain `open` would just activate *us* — no stdio wiring — and then
	// -W waits for our own exit: a self-deadlock. -n forces a fresh instance.
	args := []string{"-W", "-n", "-a", app,
		"--stdin", tty, "--stdout", tty, "--stderr", tty,
		"--args", mode}
	if len(os.Args) > 2 {
		args = append(args, os.Args[2:]...) // forward extras
	}
	// `open -W` waits but swallows the child's exit code, and our caller (e.g.
	// install.sh) needs the real one — so the child writes its code to a temp
	// file (-status-file, see main.writeStatusFile) that we read back and
	// return as our own.
	status, err := os.CreateTemp("", "zee-status-*")
	if err == nil {
		status.Close()
		defer os.Remove(status.Name())
		args = append(args, "-status-file", status.Name())
	}
	cmd := exec.Command("open", args...)
	cmd.Stderr = os.Stderr // surface `open` errors; the child writes to the tty directly
	if err := cmd.Run(); err != nil {
		return 0, false // open failed — fall back to running in place
	}
	if status != nil {
		if data, err := os.ReadFile(status.Name()); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				return n, true
			}
		}
	}
	return 1, true // child never wrote a status (crash, force-quit): not a success
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

// otherZeeRunning reports whether another zee process (a tray instance) is
// running. The wizard and doctor refuse to start alongside one — it holds the
// global hotkey their tests need — rather than killing it out from under the
// user. Matched by exact process name, excluding this process.
func otherZeeRunning() bool {
	out, err := exec.Command("pgrep", "-x", "zee").Output()
	if err != nil {
		return false // no zee processes
	}
	self := os.Getpid()
	for _, f := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(f); err == nil && pid != self {
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
