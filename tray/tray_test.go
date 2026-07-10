package tray

import (
	"testing"

	"zee/transcriber"
)

// TestEffectiveLang covers the language-preference derivation: a model that
// can't offer the user's intended language falls back to its own default, but
// the intent is never mutated — so switching back to a capable model restores
// it. This is the regression guard for the bug where selecting an English-only
// model (e.g. Parakeet) permanently clobbered a saved Auto-detect / Turkish
// choice.
func TestEffectiveLang(t *testing.T) {
	multilingual := []transcriber.Language{{Code: ""}, {Code: "en"}, {Code: "tr"}}
	englishOnly := []transcriber.Language{{Code: "en"}}

	tests := []struct {
		name   string
		intent string
		langs  []transcriber.Language
		want   string
	}{
		{"auto-detect kept on multilingual", "", multilingual, ""},
		{"auto-detect falls back to en on english-only", "", englishOnly, "en"},
		{"turkish kept on multilingual", "tr", multilingual, "tr"},
		{"turkish falls back to en on english-only", "tr", englishOnly, "en"},
		{"english passes through", "en", englishOnly, "en"},
		{"empty language set yields auto-detect", "tr", nil, ""},
	}

	for _, tt := range tests {
		if got := effectiveLang(tt.intent, tt.langs); got != tt.want {
			t.Errorf("%s: effectiveLang(%q, ...) = %q, want %q", tt.name, tt.intent, got, tt.want)
		}
	}
}

// TestEffectiveLangRoundTrip is the crux of the fix: forcing a fallback on an
// English-only model must leave the intent untouched so the next capable model
// gets it back. effectiveLang is pure, so we assert the round-trip at the value
// level.
func TestEffectiveLangRoundTrip(t *testing.T) {
	multilingual := []transcriber.Language{{Code: ""}, {Code: "tr"}}
	englishOnly := []transcriber.Language{{Code: "en"}}

	intent := "tr"
	if got := effectiveLang(intent, englishOnly); got != "en" {
		t.Fatalf("english-only should force en, got %q", got)
	}
	// intent is a plain value the caller preserves; the capable model restores it.
	if got := effectiveLang(intent, multilingual); got != "tr" {
		t.Fatalf("switching back should restore tr, got %q", got)
	}
}
