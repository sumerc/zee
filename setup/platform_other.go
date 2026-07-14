//go:build !darwin

package setup

// Non-macOS: no TCC re-exec dance and no .app bundle to launch/quit.

func maybeReexec() (code int, done bool) { return 0, false }
func isRespawnedChild() bool             { return false }
func launchInstalledApp() bool           { return false }
func otherZeeRunning() bool              { return false }
