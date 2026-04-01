package main

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
	settingsMu sync.Mutex
	current    Settings
	cfgDir     string
)

var settingsDefaults = Settings{
	Language:  "en",
	AutoPaste: true,
}

func settingsDir() string {
	if cfgDir != "" {
		return cfgDir
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
	return filepath.Join(settingsDir(), settingsFile)
}

func loadSettings() error {
	cfgDir = settingsDir()
	current = settingsDefaults

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
		current.Language = settingsDefaults.Language
	}
	return nil
}

func getSettings() Settings {
	settingsMu.Lock()
	s := current
	settingsMu.Unlock()
	return s
}

func updateSettings(fn func(*Settings)) {
	settingsMu.Lock()
	fn(&current)
	s := current
	settingsMu.Unlock()

	saveSettings(s)
}

func saveSettings(s Settings) {
	dir := cfgDir
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Warnf("settings: create dir: %v", err)
		return
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		log.Warnf("settings: marshal: %v", err)
		return
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".config-*.json")
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
