//go:build darwin

package login

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"zee/config"
)

const (
	labelApp = "com.zee.app"     // installed /Applications/Zee.app
	labelDev = "com.zee.app.dev" // local dev build
)

func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// label keys the login item (launchd Label, plist filename, target binary) off
// config.IsAppBundle so a dev build never clobbers — or gets clobbered by — the
// installed app's entry. Only Disable reaches the dev name now (see login.go):
// a dev build never registers one, it only cleans up its own.
func label() string {
	if config.IsAppBundle() {
		return labelApp
	}
	return labelDev
}

func plistName() string { return label() + ".plist" }

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", plistName()), nil
}

func enabled() bool {
	path, err := plistPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func enable() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	// No EnvironmentVariables: API keys come from credentials.json, read by the
	// app at startup regardless of how it was launched.
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
</dict>
</plist>
`, label(), xmlEscape(exe))

	path, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}

	// Writing the plist is the whole job: launchd loads ~/Library/LaunchAgents
	// at login, and RunAtLoad starts zee there. Deliberately no `launchctl
	// bootstrap` — it honors RunAtLoad immediately, so ticking the tray toggle
	// would spawn a second instance beside the running one.
	if err := os.WriteFile(path, []byte(plist), 0600); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	return nil
}

func disable() error {
	path, err := plistPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	exec.Command("launchctl", "bootout", domain, path).Run()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}
	return nil
}
