//go:build darwin

package login

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	plistNameApp = "com.zee.app.plist"     // installed /Applications/Zee.app
	plistNameDev = "com.zee.app.dev.plist" // local dev build
	bundleSig    = ".app/Contents/MacOS/"
)

func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// isRunningFromApp reports whether this binary is the installed Zee.app bundle
// rather than a local dev build. The login item (plist filename, launchd Label,
// and target binary) is keyed off this so a dev build never clobbers — or gets
// clobbered by — the installed app's entry.
func isRunningFromApp() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.Contains(exe, bundleSig)
}

func plistName() string {
	if isRunningFromApp() {
		return plistNameApp
	}
	return plistNameDev
}

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", plistName()), nil
}

func Enabled() bool {
	path, err := plistPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
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
			fmt.Fprintf(&env, "\t\t\t<key>%s</key>\n\t\t\t<string>%s</string>\n", key, xmlEscape(v))
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
`, plistName(), xmlEscape(exe), env.String())

	path, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}

	if err := os.WriteFile(path, []byte(plist), 0600); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	domain := fmt.Sprintf("gui/%d", os.Getuid())
	exec.Command("launchctl", "bootout", domain, path).Run()
	if out, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w (%s)", err, out)
	}
	return nil
}

func Disable() error {
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
