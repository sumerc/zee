package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSettingsDefaults(t *testing.T) {
	cfgDir = t.TempDir()
	current = Settings{}

	if err := loadSettings(); err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	s := getSettings()
	if s.Language != "en" {
		t.Errorf("Language = %q, want %q", s.Language, "en")
	}
	if !s.AutoPaste {
		t.Error("AutoPaste = false, want true")
	}
	if s.Provider != "" || s.Model != "" || s.Device != "" {
		t.Errorf("expected zero-value strings, got Provider=%q Model=%q Device=%q", s.Provider, s.Model, s.Device)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	cfgDir = t.TempDir()
	current = Settings{}

	if err := loadSettings(); err != nil {
		t.Fatalf("loadSettings: %v", err)
	}

	updateSettings(func(s *Settings) {
		s.Language = "fr"
		s.Device = "Blue Yeti"
		s.Provider = "groq"
		s.Model = "whisper-large-v3-turbo"
		s.AutoPaste = false
		s.AutoStart = true
	})

	// Re-load from disk
	current = Settings{}
	if err := loadSettings(); err != nil {
		t.Fatalf("loadSettings after update: %v", err)
	}

	s := getSettings()
	if s.Language != "fr" {
		t.Errorf("Language = %q, want %q", s.Language, "fr")
	}
	if s.Device != "Blue Yeti" {
		t.Errorf("Device = %q, want %q", s.Device, "Blue Yeti")
	}
	if s.Provider != "groq" {
		t.Errorf("Provider = %q, want %q", s.Provider, "groq")
	}
	if s.Model != "whisper-large-v3-turbo" {
		t.Errorf("Model = %q, want %q", s.Model, "whisper-large-v3-turbo")
	}
	if s.AutoPaste {
		t.Error("AutoPaste = true, want false")
	}
	if !s.AutoStart {
		t.Error("AutoStart = false, want true")
	}
}

func TestSettingsCopySafety(t *testing.T) {
	cfgDir = t.TempDir()
	current = settingsDefaults

	s := getSettings()
	s.Language = "xx"

	s2 := getSettings()
	if s2.Language == "xx" {
		t.Error("mutating returned Settings affected internal state")
	}
}

func TestSettingsCorruptFile(t *testing.T) {
	cfgDir = t.TempDir()
	current = Settings{}

	os.WriteFile(filepath.Join(cfgDir, settingsFile), []byte("not json{{{"), 0644)

	if err := loadSettings(); err != nil {
		t.Fatalf("loadSettings should not error on corrupt file: %v", err)
	}
	s := getSettings()
	if s.Language != "en" {
		t.Errorf("Language = %q, want default %q after corrupt file", s.Language, "en")
	}
}

func TestSettingsConcurrent(t *testing.T) {
	cfgDir = t.TempDir()
	current = settingsDefaults

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			updateSettings(func(s *Settings) { s.Language = "es" })
		}()
		go func() {
			defer wg.Done()
			_ = getSettings()
		}()
	}
	wg.Wait()
}
