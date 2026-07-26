package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"zee/hotkey"
	"zee/log"
)

// Hotkey is the saved push-to-talk combination. Key is the platform-native
// keycode; an empty Hotkey (no Mods) means "use the built-in default"
// (hotkey.Combo.OrDefault resolves that).
type Settings struct {
	Language  string       `json:"language"`
	Device    string       `json:"device"`
	Provider  string       `json:"provider"`
	Model     string       `json:"model"`
	Hotkey    hotkey.Combo `json:"hotkey"`
	AutoPaste bool         `json:"auto_paste"`
	AutoStart bool         `json:"auto_start"`
	// TailWaitMs keeps the mic open this many ms after the hotkey is released so
	// a fast keyup doesn't clip the last word. 0 disables the wait.
	TailWaitMs int `json:"tail_wait_ms"`
}

const settingsFile = "config.json"

// defaultTailWaitMs is the default mic tail-wait after hotkey release (ms):
// long enough to catch the last word on a fast keyup, short enough not to feel
// like lag before inference. History: halved to 50 for felt latency (release →
// text, see log.ReleaseToText), reverted to 100 when recordings seemed clipped
// — but that clipping matched the whisper no_timestamps tail-drop bug (see
// CHANGELOG), so with that fixed, 50 is back on trial. See Settings.TailWaitMs.
const defaultTailWaitMs = 50

var (
	mu       sync.Mutex
	current  Settings
	dir      string
	defaults = Settings{
		Language:   "en",
		AutoPaste:  true,
		TailWaitMs: defaultTailWaitMs,
	}
)

// dirMu guards dir on its own lock (not mu): reload() runs under mu and calls
// Dir() via settingsPath(), so sharing mu would deadlock.
var dirMu sync.Mutex

func SetDir(d string) {
	dirMu.Lock()
	dir = d
	dirMu.Unlock()
}

func Dir() string {
	dirMu.Lock()
	defer dirMu.Unlock()
	if dir == "" {
		dir = resolveDir()
	}
	return dir
}

func resolveDir() string {
	// ZEE_CONFIG_DIR overrides the location entirely (integration tests seed a
	// credentials.json / config.json there; mirrors ZEE_MODELS_DIR, ZEE_LOG_PATH).
	if d := os.Getenv("ZEE_CONFIG_DIR"); d != "" {
		return d
	}
	// macOS dev builds keep all state next to the binary (<exe dir>/.zee) so a
	// working copy is fully self-contained and never collides with the installed
	// app's per-user config. Mirrors localmodel.Dir()'s dev/app split. The
	// installed .app falls through to the OS-standard per-user location below.
	// Darwin-only: IsAppBundle can never be true elsewhere, and a packaged
	// Linux binary in /usr/local/bin must not try to write /usr/local/bin/.zee.
	if runtime.GOOS == "darwin" && !IsAppBundle() {
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

// AppBundlePath returns the <...>.app directory containing this executable and
// whether it is one at all. It owns the single ".app/Contents/MacOS/" marker in
// the codebase — setup (relaunching the installed app) and update (swapping the
// bundle) both ask here rather than re-deriving it.
func AppBundlePath() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	const marker = ".app/Contents/MacOS/"
	i := strings.Index(exe, marker)
	if i < 0 {
		return "", false
	}
	return exe[:i+len(".app")], true
}

// IsAppBundle reports whether this binary is the installed Zee.app rather than a
// local dev build, keyed off the executable path (not cwd). It's the single
// "am I the installed app?" signal — used to pick app-vs-dev locations
// consistently (login-item plist, local models dir).
func IsAppBundle() bool {
	_, ok := AppBundlePath()
	return ok
}

func Load() error {
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

// Reload re-reads config.json into the live settings — the user-initiated
// tray "Reload Config" flow (there is deliberately no file watcher: a manual
// reload runs under the same busy-guard as every other engine op and can
// report a parse failure to the user's face). Read + compare + swap all happen
// under the lock, serialized with Update, so a concurrent save can't be
// overwritten by stale file contents. A missing or corrupt file leaves the
// live settings untouched and is returned as an error for the UI to show.
func Reload() (Settings, error) {
	mu.Lock()
	defer mu.Unlock()
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		return current, err
	}
	s := defaults
	if err := json.Unmarshal(data, &s); err != nil {
		return current, fmt.Errorf("invalid config.json: %w", err)
	}
	current = s
	return s, nil
}

func Update(fn func(*Settings)) {
	mu.Lock()
	fn(&current)
	s := current
	mu.Unlock()

	save(s)
}

func save(s Settings) {
	d := Dir()
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
