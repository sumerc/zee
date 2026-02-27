//go:build darwin

package login

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	var envEntries string
	for _, key := range []string{"GROQ_API_KEY", "DEEPGRAM_API_KEY"} {
		if v := os.Getenv(key); v != "" {
			envEntries += fmt.Sprintf("\t\t\t<key>%s</key>\n\t\t\t<string>%s</string>\n", key, v)
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
		<key>_ZEE_BG</key>
		<string>1</string>
%s	</dict>
</dict>
</plist>
`, plistName, exe, envEntries)

	dir := filepath.Dir(plistPath())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}

	if err := os.WriteFile(plistPath(), []byte(plist), 0644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	if out, err := exec.Command("launchctl", "load", plistPath()).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load: %w (%s)", err, out)
	}
	return nil
}

func Disable() error {
	path := plistPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	exec.Command("launchctl", "unload", path).Run()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}
	return nil
}
