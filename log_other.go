//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"runtime"
)

func getDefaultLogDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Logs", "zee"), nil
	}

	// Linux: XDG_CONFIG_HOME (Tauri convention)
	xdgConfig := os.Getenv("XDG_CONFIG_HOME")
	if xdgConfig == "" {
		xdgConfig = filepath.Join(home, ".config")
	}
	return filepath.Join(xdgConfig, "zee", "logs"), nil
}
