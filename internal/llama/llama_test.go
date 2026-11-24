package llama

import (
	"os"
	"strings"
	"testing"
)

// TestGenerate loads the GGUF named by ZEE_LLAMA_MODEL and checks one Gemma 4
// turn comes back as plain text, ended by the model (a reply cut off before
// end-of-generation is an error). Skipped when the model is not on disk.
func TestGenerate(t *testing.T) {
	path := os.Getenv("ZEE_LLAMA_MODEL")
	if path == "" || !Available() {
		t.Skip("ZEE_LLAMA_MODEL not set or llama not compiled in")
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	prompt := "<|turn>system\nReply with the cleaned text only.<turn|>\n<|turn>user\nhello their how are you<turn|>\n<|turn>model\n"
	var replies [2]string
	for i := range replies {
		out, err := m.Generate(prompt, 64)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(out) == "" {
			t.Fatal("empty reply")
		}
		replies[i] = out
		t.Logf("reply: %q", out)
	}
	// Greedy decoding is deterministic: a second reply that differs saw the
	// first one's KV cache.
	if replies[0] != replies[1] {
		t.Errorf("replies differ: %q, %q", replies[0], replies[1])
	}
	if out, err := m.Generate(prompt, 2); err == nil {
		t.Errorf("a reply cut off at 2 tokens returned %q, want an error", out)
	}
}
