//go:build darwin

package login

import (
	"os"
	"path/filepath"
	"testing"
)

// A dev build must not register a login item. The bug this pins: a dev config
// carrying auto_start:true made Reload Config call Enable(), which wrote the
// plist and ran `launchctl bootstrap` — RunAtLoad then had launchd start a
// SECOND dev instance immediately. Being a launchd process rather than a child
// of the terminal, it re-prompted for Microphone/Accessibility (and the Desktop
// folder, since a dev config dir lives under the working copy).
func TestEnableIsNoOpForDevBuild(t *testing.T) {
	if Supported() {
		t.Skip("test binary is an app bundle")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := Enable(); err != nil {
		t.Fatalf("Enable() = %v, want nil (silent no-op)", err)
	}
	if Enabled() {
		t.Error("Enabled() = true after a no-op Enable()")
	}
	entries, err := os.ReadDir(filepath.Join(home, "Library", "LaunchAgents"))
	if err == nil && len(entries) > 0 {
		t.Errorf("Enable() wrote %d LaunchAgents entry/entries: %v", len(entries), entries)
	}
}

// Disable stays reachable for a dev build so it can drop a plist an earlier
// build of itself registered.
func TestDisableCleansUpDevPlist(t *testing.T) {
	if Supported() {
		t.Skip("test binary is an app bundle")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	agents := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agents, 0755); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(agents, plistName())
	if err := os.WriteFile(plist, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := Disable(); err != nil {
		t.Fatalf("Disable() = %v", err)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Errorf("plist still present after Disable(): %v", err)
	}
}
