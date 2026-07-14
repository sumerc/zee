//go:build darwin

package setup

/*
#include <spawn.h>
#include <stdlib.h>
#include <dlfcn.h>

// responsibility_spawnattrs_setdisclaim is a private libSystem SPI (stable
// since 10.14; Chromium/LLDB ship on it). It marks the spawnattrs so the child
// becomes its own "responsible process" for TCC — permission prompts name the
// child's bundle (Zee), not the terminal that transitively spawned it. This is
// the one thing `open` gives us that a plain exec cannot.
//
// Resolved via dlsym, NOT direct linking: a private symbol that dyld had to
// bind at load could make Zee fail to launch outright on a future macOS that
// renamed/removed it, before any Go-side fallback runs. dlsym keeps the
// dependency soft — absent symbol → SPAWN_NO_SYMBOL, caller degrades to
// running in place.
typedef int (*setdisclaim_fn)(posix_spawnattr_t *, int);

#define SPAWN_NO_SYMBOL -1

static int spawn_disclaimed(const char *path, char *const argv[], char *const envp[], int *pid_out) {
	setdisclaim_fn setdisclaim =
		(setdisclaim_fn)dlsym(RTLD_DEFAULT, "responsibility_spawnattrs_setdisclaim");
	if (setdisclaim == NULL) return SPAWN_NO_SYMBOL;

	posix_spawnattr_t attr;
	pid_t pid;
	int rc = posix_spawnattr_init(&attr);
	if (rc != 0) return rc;
	rc = setdisclaim(&attr, 1);
	if (rc == 0) rc = posix_spawn(&pid, path, NULL, &attr, argv, envp);
	posix_spawnattr_destroy(&attr);
	if (rc == 0) *pid_out = (int)pid;
	return rc;
}
*/
import "C"

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

// errNoDisclaimSymbol signals the SPI was absent at runtime — maybeReexec then
// degrades to running the wizard in place (terminal-attributed) rather than
// failing.
var errNoDisclaimSymbol = errors.New("responsibility_spawnattrs_setdisclaim unavailable")

// disclaimedEnv marks the respawned child so it doesn't respawn again.
const disclaimedEnv = "ZEE_DISCLAIMED"

// spawnDisclaimed re-runs this executable with the same args, disclaiming TCC
// responsibility so the child — not the terminal — owns its permission
// prompts. stdio is inherited (the child stays on this tty) and the child's
// exit code is returned directly: no LaunchServices, no status-file dance.
func spawnDisclaimed() (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	argv := append([]string{exe}, os.Args[1:]...)
	envp := append(os.Environ(), disclaimedEnv+"=1")

	cArgv := make([]*C.char, len(argv)+1) // NULL-terminated
	for i, a := range argv {
		cArgv[i] = C.CString(a)
	}
	cEnvp := make([]*C.char, len(envp)+1)
	for i, e := range envp {
		cEnvp[i] = C.CString(e)
	}
	defer func() {
		for _, p := range cArgv[:len(argv)] {
			C.free(unsafe.Pointer(p))
		}
		for _, p := range cEnvp[:len(envp)] {
			C.free(unsafe.Pointer(p))
		}
	}()

	var pid C.int
	if rc := C.spawn_disclaimed(cArgv[0], &cArgv[0], &cEnvp[0], &pid); rc != 0 {
		if rc == C.SPAWN_NO_SYMBOL {
			return 0, errNoDisclaimSymbol
		}
		return 0, syscall.Errno(rc)
	}

	var ws syscall.WaitStatus
	for {
		if _, err := syscall.Wait4(int(pid), &ws, 0, nil); err != syscall.EINTR {
			if err != nil {
				return 0, err
			}
			break
		}
	}
	if ws.Exited() {
		return ws.ExitStatus(), nil
	}
	return 1, nil // killed by signal: not a success
}
