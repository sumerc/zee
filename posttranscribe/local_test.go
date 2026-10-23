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

// TestRewriteLocalMissing: a local model that is not on disk fails with a
// message that says where to put it, and needs no API key.
func TestRewriteLocalMissing(t *testing.T) {
	m, _ := localmodel.ByID(localmodel.IDGemma4E2B)
	if localmodel.Present(m) {
		t.Skip("model is on disk")
	}
	_, err := Rewrite(config.PostTranscribe{Active: "clean", Model: m.ID}, "", "en", "x")
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
	out, err := Rewrite(config.PostTranscribe{Active: "clean", Model: m.ID}, "", "en", "um so hello their how are you")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "<turn|>") || strings.Contains(strings.ToLower(out), "their") {
		t.Errorf("out = %q", out)
	}
	t.Logf("out: %q", out)
}
