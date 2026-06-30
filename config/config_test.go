package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSettingsDefaults(t *testing.T) {
	SetDir(t.TempDir())

	if err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := Get()
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
	SetDir(t.TempDir())

	if err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	Update(func(s *Settings) {
		s.Language = "fr"
		s.Device = "Blue Yeti"
		s.Provider = "groq"
		s.Model = "whisper-large-v3-turbo"
		s.AutoPaste = false
		s.AutoStart = true
	})

	if err := Load(); err != nil {
		t.Fatalf("Load after update: %v", err)
	}

	s := Get()
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

// TestAutoDetectLanguagePersists reproduces the bug where selecting Auto-detect
// (empty language code) is not remembered across restarts: Load coerced the
// saved "" back to the default "en", so auto-detect silently reverted to
// English. After the fix an explicit empty language round-trips.
func TestAutoDetectLanguagePersists(t *testing.T) {
	SetDir(t.TempDir())
	if err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	Update(func(s *Settings) { s.Language = "" }) // user picks Auto-detect
	if err := Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := Get().Language; got != "" {
		t.Fatalf("auto-detect not persisted: Language = %q, want \"\"", got)
	}
}

// TestMissingLanguageDefaultsToEn guards that a config file with no language
// field still falls back to "en" (so the fix above doesn't turn unset into
// auto-detect).
func TestMissingLanguageDefaultsToEn(t *testing.T) {
	d := t.TempDir()
	SetDir(d)
	os.WriteFile(filepath.Join(d, "config.json"), []byte(`{"device":"x"}`), 0644)
	if err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := Get().Language; got != "en" {
		t.Fatalf("missing language should default to en, got %q", got)
	}
}

func TestSettingsCopySafety(t *testing.T) {
	SetDir(t.TempDir())
	Load()

	s := Get()
	s.Language = "xx"

	s2 := Get()
	if s2.Language == "xx" {
		t.Error("mutating returned Settings affected internal state")
	}
}

func TestSettingsCorruptFile(t *testing.T) {
	d := t.TempDir()
	SetDir(d)

	os.WriteFile(filepath.Join(d, "config.json"), []byte("not json{{{"), 0644)

	if err := Load(); err != nil {
		t.Fatalf("Load should not error on corrupt file: %v", err)
	}
	s := Get()
	if s.Language != "en" {
		t.Errorf("Language = %q, want default %q after corrupt file", s.Language, "en")
	}
}

func TestSettingsConcurrent(t *testing.T) {
	SetDir(t.TempDir())
	Load()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			Update(func(s *Settings) { s.Language = "es" })
		}()
		go func() {
			defer wg.Done()
			_ = Get()
		}()
	}
	wg.Wait()
}
