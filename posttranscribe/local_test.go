package posttranscribe

import (
	"strings"
	"testing"

	"zee/config"
	"zee/internal/llama"
	"zee/localmodel"
)

func TestLocalEntry(t *testing.T) {
	if _, ok := localEntry(localmodel.IDGemma4E2B); !ok {
		t.Errorf("%s is not a local post-transcribe model", localmodel.IDGemma4E2B)
	}
	for _, id := range []string{DefaultModel, localmodel.IDWhisperQ5, ""} {
		if _, ok := localEntry(id); ok {
			t.Errorf("localEntry(%q) = true", id)
		}
	}
}

// TestLocalPrompt: the language line sits between the rules and the
// instruction, the place it was measured in, and the turn is Gemma 4's.
func TestLocalPrompt(t *testing.T) {
	sys := systemPrompt(Builtin["clean"], "", "Turkish")
	if !strings.Contains(sys, "tags.\nThe transcript is in Turkish.\n\nInstruction: ") {
		t.Errorf("language line misplaced:\n%s", sys)
	}
	p := gemma4Prompt("S", "U")
	if p != "<|turn>system\nS<turn|>\n<|turn>user\nU<turn|>\n<|turn>model\n" {
		t.Errorf("gemma4Prompt = %q", p)
	}
}

// TestProvider: the provider picks the backend and its default model, and a
// model that does not fit the provider is an error, not a request to Groq.
func TestProvider(t *testing.T) {
	for _, tc := range []struct {
		c         config.PostTranscribe
		model     string
		isLocal   bool
		errSubstr string
	}{
		{config.PostTranscribe{}, DefaultModel, false, ""},
		{config.PostTranscribe{Provider: ProviderGroq, Model: "qwen/x"}, "qwen/x", false, ""},
		{config.PostTranscribe{Provider: ProviderLocal}, DefaultLocalModel, true, ""},
		{config.PostTranscribe{Provider: ProviderLocal, Model: DefaultModel}, DefaultModel, false, "not a local model"},
		{config.PostTranscribe{Model: DefaultLocalModel}, DefaultLocalModel, false, "set post_transcribe.provider"},
		{config.PostTranscribe{Provider: "ollama"}, DefaultModel, false, "unknown post_transcribe provider"},
	} {
		if got := Model(tc.c); got != tc.model {
			t.Errorf("Model(%+v) = %q, want %q", tc.c, got, tc.model)
		}
		_, isLocal, err := local(tc.c)
		if isLocal != tc.isLocal || (err == nil) != (tc.errSubstr == "") ||
			(err != nil && !strings.Contains(err.Error(), tc.errSubstr)) {
			t.Errorf("local(%+v) = %v, %v; want %v, error with %q", tc.c, isLocal, err, tc.isLocal, tc.errSubstr)
		}
	}
	// The mismatch fails before any request is sent.
	_, err := Rewrite(config.PostTranscribe{Active: "clean", Model: DefaultLocalModel}, "", "", "x")
	if err == nil {
		t.Error("Rewrite with a local model on Groq succeeded")
	}
}

// TestRewriteLocalMissing: a local model that is not on disk fails with a
// message that says where to put it, and needs no API key.
func TestRewriteLocalMissing(t *testing.T) {
	m, _ := localmodel.ByID(localmodel.IDGemma4E2B)
	if localmodel.Present(m) {
		t.Skip("model is on disk")
	}
	_, err := Rewrite(config.PostTranscribe{Active: "clean", Provider: ProviderLocal}, "", "en", "x")
	if err == nil || !strings.Contains(err.Error(), m.Filename) {
		t.Errorf("err = %v, want a not-downloaded error naming %s", err, m.Filename)
	}
}

// TestRewriteLocal runs a real rewrite when the model is downloaded.
func TestRewriteLocal(t *testing.T) {
	m, _ := localmodel.ByID(localmodel.IDGemma4E2B)
	if !llama.Available() || !localmodel.Present(m) {
		t.Skip("local LLM not compiled in or not downloaded")
	}
	out, err := Rewrite(config.PostTranscribe{Active: "clean", Provider: ProviderLocal}, "", "en", "um so hello their how are you")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "<turn|>") || strings.Contains(strings.ToLower(out), "their") {
		t.Errorf("out = %q", out)
	}
	t.Logf("out: %q", out)
}
