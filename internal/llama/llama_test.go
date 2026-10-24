package llama

import (
	"os"
	"strings"
	"testing"
)

// TestGenerate loads the GGUF named by ZEE_LLAMA_MODEL and checks one Gemma 4
// turn comes back as plain text. Skipped when the model is not on disk.
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
	for range 2 { // the second call must not see the first one's KV cache
		out, err := m.Generate(prompt, 64)
		if err != nil {
			t.Fatal(err)
		}
		if out = strings.TrimSpace(out); out == "" || strings.Contains(out, "<turn|>") {
			t.Fatalf("reply = %q", out)
		}
		t.Logf("reply: %q", out)
	}
}
