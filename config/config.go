package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"zee/log"
)

// Hotkey is the saved push-to-talk combination. Key is the platform-native
// keycode; an empty Hotkey (no Mods) means "use the built-in default".
type Hotkey struct {
	Mods  []string `json:"mods"`
	Key   int      `json:"key"`
	Label string   `json:"label"`
}

type Settings struct {
	Language  string `json:"language"`
	Device    string `json:"device"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Hotkey    Hotkey `json:"hotkey"`
	AutoPaste bool   `json:"auto_paste"`
	AutoStart bool   `json:"auto_start"`
}

const settingsFile = "config.json"

var (
	mu       sync.Mutex
	current  Settings
	dir      string
	defaults = Settings{
		Language:  "en",
		AutoPaste: true,
	}
)

func SetDir(d string) { dir = d }

func Dir() string {
	if dir != "" {
		return dir
	}
	// ZEE_CONFIG_DIR overrides the location entirely (integration tests seed a
	// credentials.json / config.json there; mirrors ZEE_MODELS_DIR, ZEE_LOG_PATH).
	if d := os.Getenv("ZEE_CONFIG_DIR"); d != "" {
		return d
	}
	// Dev builds keep all state next to the binary (<exe dir>/.zee) so a working
	// copy is fully self-contained and never collides with the installed app's
	// per-user config. Mirrors localmodel.Dir()'s dev/app split. The installed
	// .app falls through to the OS-standard per-user location below.
	if !IsAppBundle() {
		if exe, err := os.Executable(); err == nil {
			return filepath.Join(filepath.Dir(exe), ".zee")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "zee")
	case "windows":
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			return filepath.Join(v, "zee")
		}
		return filepath.Join(home, "AppData", "Local", "zee")
	default:
		xdg := os.Getenv("XDG_CONFIG_HOME")
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		return filepath.Join(xdg, "zee")
	}
}

func settingsPath() string {
	return filepath.Join(Dir(), settingsFile)
}

// SettingsPath is the on-disk config.json path (for the tray "Edit Settings…"
// item to open in an editor).
func SettingsPath() string { return settingsPath() }

// IsAppBundle reports whether this binary is the installed Zee.app rather than a
// local dev build, keyed off the executable path (not cwd). It's the single
// "am I the installed app?" signal — used to pick app-vs-dev locations
// consistently (login-item plist, local models dir).
func IsAppBundle() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.Contains(exe, ".app/Contents/MacOS/")
}

func Load() error {
	dir = Dir()
	current = defaults

	data, err := os.ReadFile(settingsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// Unmarshal into a defaults-seeded copy so fields absent from the file keep
	// their defaults, while fields present in the file win. This distinguishes
	// "language not set" (→ default "en") from an explicit "language":""
	// (Auto-detect), which must persist.
	s := defaults
	if err := json.Unmarshal(data, &s); err != nil {
		log.Warnf("settings: corrupt config.json, using defaults: %v", err)
		return nil
	}
	current = s
	return nil
}

func Get() Settings {
	mu.Lock()
	s := current
	mu.Unlock()
	return s
}

// EnsureSaved writes config.json (creating its directory) if it does not already
// exist, so external tools — the tray "Edit Settings…" item — always have a file
// to open. config.Load never creates the file, so a fresh install has none until
// the first setting changes.
func EnsureSaved() {
	if _, err := os.Stat(settingsPath()); err == nil {
		return
	}
	mu.Lock()
	s := current
	mu.Unlock()
	save(s)
}

func Update(fn func(*Settings)) {
	mu.Lock()
	fn(&current)
	s := current
	mu.Unlock()

	save(s)
}

func save(s Settings) {
	d := dir
	if err := os.MkdirAll(d, 0755); err != nil {
		log.Warnf("settings: create dir: %v", err)
		return
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		log.Warnf("settings: marshal: %v", err)
		return
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(d, ".config-*.json")
	if err != nil {
		log.Warnf("settings: create temp: %v", err)
		return
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		log.Warnf("settings: write temp: %v", err)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		log.Warnf("settings: close temp: %v", err)
		return
	}

	if err := os.Rename(tmpPath, settingsPath()); err != nil {
		os.Remove(tmpPath)
		log.Warnf("settings: rename: %v", err)
	}
}
