//go:build darwin

package login

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const plistName = "com.zee.app.plist"

func plistPath() string {
	return filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents", plistName)
}

func Enabled() bool {
	_, err := os.Stat(plistPath())
	return err == nil
}

func Enable() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	var env strings.Builder
	for _, key := range []string{"GROQ_API_KEY", "OPENAI_API_KEY", "DEEPGRAM_API_KEY"} {
		if v := os.Getenv(key); v != "" {
			fmt.Fprintf(&env, "\t\t\t<key>%s</key>\n\t\t\t<string>%s</string>\n", key, html.EscapeString(v))
		}
	}

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
	<key>EnvironmentVariables</key>
	<dict>
%s	</dict>
</dict>
</plist>
`, plistName, html.EscapeString(exe), env.String())

	path := plistPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}

	if err := os.WriteFile(path, []byte(plist), 0600); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	domain := fmt.Sprintf("gui/%d", os.Getuid())
	// Bootout first in case service is already loaded (re-enable scenario)
	exec.Command("launchctl", "bootout", domain, path).Run()
	if out, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w (%s)", err, out)
	}
	return nil
}

func Disable() error {
	path := plistPath()
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
