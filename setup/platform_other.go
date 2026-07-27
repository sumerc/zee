//go:build !darwin

package setup

// Non-macOS: no TCC re-exec dance and no .app bundle to launch/quit.

import "fmt"

func maybeReexec() (code int, done bool) { return 0, false }
func isRespawnedChild() bool             { return false }
func launchInstalledApp() bool           { return false }
func otherZeeRunning() bool              { return false }

// SpawnSetupAt only exists for the macOS update handoff; update.Install
// already errors on other platforms before this could be reached.
func SpawnSetupAt(string) (int, error) {
	return 0, fmt.Errorf("update setup handoff is macOS-only")
}
