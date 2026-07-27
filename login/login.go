// Package login manages the "start zee when I log in" system entry.
package login

import "zee/config"

// Supported reports whether auto-start applies to this build. It is an
// installed-app-only feature: a dev build's entry would point at a working-copy
// binary that is rebuilt and re-signed constantly, so the OS launches a stale
// binary at login and macOS re-prompts for Microphone/Accessibility each time
// the signature changes. Callers should grey out the toggle rather than offer a
// switch that does nothing.
func Supported() bool { return config.IsAppBundle() }

// Enabled reports whether zee is registered to start at login.
func Enabled() bool { return Supported() && enabled() }

// Enable registers this binary to start at login. It is a no-op where
// auto-start is unsupported.
func Enable() error {
	if !Supported() {
		return nil
	}
	return enable()
}

// Disable removes the entry. Deliberately unguarded: a dev build must still be
// able to clean up an entry that an earlier build of itself registered.
func Disable() error { return disable() }
