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

// TestChoosePostNoted: a click on an entry with a note (its model is not
// downloaded) is reported but keeps the checkmark where it was.
func TestChoosePostNoted(t *testing.T) {
	var got []string
	SetPostTranscribe([]PostProfile{{Name: "clean", Note: "download 2.4 GB"}}, "", func(name string) { got = append(got, name) })
	t.Cleanup(func() {
		SetPostTranscribe(nil, "", nil)
		trayMu.Lock()
		postCb = nil
		trayMu.Unlock()
	})

	choosePost(1)

	if len(got) != 1 || got[0] != "clean" {
		t.Fatalf("callbacks = %q, want [clean]", got)
	}
	if postActive != "" {
		t.Errorf("postActive = %q, want None kept", postActive)
	}
}

// TestChoosePostNoKey: a profile disabled for a missing API key ignores a
// click: nothing is reported and None stays selected.
func TestChoosePostNoKey(t *testing.T) {
	var got []string
	SetPostTranscribe([]PostProfile{{Name: "clean", NoKey: true}}, "", func(name string) { got = append(got, name) })
	t.Cleanup(func() {
		SetPostTranscribe(nil, "", nil)
		trayMu.Lock()
		postCb = nil
		trayMu.Unlock()
	})

	choosePost(1)

	if len(got) != 0 || postActive != "" {
		t.Errorf("callbacks = %q, active = %q; want none and None", got, postActive)
	}
}

// TestChoosePost: entry 0 is None (""), the rest follow the profiles; a click
// reports the choice and moves the selection.
func TestChoosePost(t *testing.T) {
	var got []string
	SetPostTranscribe([]PostProfile{{Name: "friendly"}, {Name: "clean"}}, "", func(name string) { got = append(got, name) })
	t.Cleanup(func() {
		// A nil callback keeps the current one, so drop the test's here.
		SetPostTranscribe(nil, "", nil)
		trayMu.Lock()
		postCb = nil
		trayMu.Unlock()
	})

	choosePost(2)
	choosePost(0)
	choosePost(9) // past the end: ignored

	if len(got) != 2 || got[0] != "clean" || got[1] != "" {
		t.Fatalf("callbacks = %q, want [clean \"\"]", got)
	}
	if postActive != "" {
		t.Errorf("postActive = %q, want None", postActive)
	}
}

// TestChoosePostModel: like the transcription Model menu, a ready entry is
// reported and gets the checkmark; a downloadable one is reported without it;
// an unavailable (no key) or downloading one is ignored.
func TestChoosePostModel(t *testing.T) {
	var got []string
	SetPostModels([]Model{
		{Provider: "local", ModelID: "gemma", Group: "Local", State: ModelNeedsDownload, Detail: "2.4 GB"},
		{Provider: "groq", ModelID: "a", Group: "Groq"},
		{Provider: "groq", ModelID: "b", Group: "Groq", Active: true},
		{Provider: "openai", ModelID: "c", Group: "OpenAI", State: ModelUnavailable},
	}, func(provider, model string) { got = append(got, provider+":"+model) })
	t.Cleanup(func() {
		SetPostModels(nil, nil)
		trayMu.Lock()
		postModelCb = nil
		trayMu.Unlock()
	})
	active := func() string {
		trayMu.Lock()
		defer trayMu.Unlock()
		for _, m := range postModels {
			if m.Active {
				return m.Provider + ":" + m.ModelID
			}
		}
		return ""
	}

	choosePostModel(0)
	if a := active(); a != "groq:b" {
		t.Errorf("after the download click, active = %q, want groq:b kept", a)
	}
	choosePostModel(3) // no key: ignored
	choosePostModel(1)
	choosePostModel(9) // past the end: ignored

	if len(got) != 2 || got[0] != "local:gemma" || got[1] != "groq:a" {
		t.Fatalf("callbacks = %q, want [local:gemma groq:a]", got)
	}
	if a := active(); a != "groq:a" {
		t.Errorf("active = %q, want groq:a", a)
	}
}
