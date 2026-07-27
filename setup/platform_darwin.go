//go:build darwin

package setup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"zee/config"
)

// maybeReexec re-runs the `setup`/`doctor` subcommand as a TCC-disclaimed
// child (see disclaim_darwin.go) when started from a terminal as the installed
// app bundle: the child — not the terminal — then owns the Microphone /
// Accessibility prompts, so they say "Zee". stdio and the exit code inherit
// naturally. Returns done=true when it respawned (caller should exit).
//
// It is a no-op — done=false — for dev builds (TCC deliberately attributes to
// the terminal there, so grants survive rebuilds), for the disclaimed child
// itself, and if the spawn fails (run in place rather than not at all).
//
// This replaced an `open -W` LaunchServices relaunch: that needed tty wiring,
// an exit-code side channel, and collided with the direct-exec'd process
// already being registered as the running Zee.app instance.
func maybeReexec() (code int, done bool) {
	if !config.IsAppBundle() || isRespawnedChild() {
		return 0, false
	}
	exe, err := os.Executable()
	if err != nil {
		return 0, false
	}
	code, err = spawnDisclaimed(exe, os.Args[1:])
	if err != nil {
		// Symbol gone on some future macOS: degrade to running in place. Prompts
		// then attribute to the terminal — note it so it's diagnosable.
		if errors.Is(err, errNoDisclaimSymbol) {
			fmt.Fprintln(os.Stderr, "note: TCC disclaim unavailable; running setup in place (prompts may attribute to your terminal)")
		}
		return 0, false
	}
	return code, true
}

// isRespawnedChild reports whether this process is the disclaimed child — the
// instance-guard in Run/Doctor must skip it (pgrep would find its own waiting
// parent) and maybeReexec must not respawn again.
func isRespawnedChild() bool { return os.Getenv(disclaimedEnv) == "1" }

// SpawnSetupAt runs `zee setup` from the bundle at appPath as a TCC-disclaimed
// child and returns its exit code. `zee update` hands off here after swapping
// the bundle: the app is ad-hoc signed, so the swap changed the cdhash and
// macOS dropped the TCC grants — setup (running as the NEW binary at the
// installed path) re-grants and re-verifies. The disclaim spawn is required,
// not just reused: its env marker is what lets the child's instance-guard
// tolerate this waiting zee parent. If the disclaim SPI is gone, degrade to a
// plain spawn carrying the same marker (prompts then attribute to the
// terminal, matching maybeReexec's degradation).
func SpawnSetupAt(appPath string) (int, error) {
	bin := filepath.Join(appPath, "Contents", "MacOS", "zee")
	code, err := spawnDisclaimed(bin, []string{"setup"})
	if !errors.Is(err, errNoDisclaimSymbol) {
		return code, err
	}
	cmd := exec.Command(bin, "setup")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), disclaimedEnv+"=1")
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 0, err
	}
	return 0, nil
}

// launchInstalledApp starts the tray app after setup completes (installed
// bundle only; reports whether it launched). -n forces a new instance:
// without it, LaunchServices sees the still-running wizard (same bundle) and
// merely activates it, so no tray would ever start.
func launchInstalledApp() bool {
	app, ok := config.AppBundlePath()
	if !ok {
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
