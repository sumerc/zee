package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"zee/log"
)

type Settings struct {
	Language  string `json:"language"`
	Device    string `json:"device"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
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

	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		log.Warnf("settings: corrupt config.json, using defaults: %v", err)
		return nil
	}

	current = s
	if current.Language == "" {
		current.Language = defaults.Language
	}
	return nil
}

func Get() Settings {
	mu.Lock()
	s := current
	mu.Unlock()
	return s
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
