package setup

import (
	"testing"
)

// These run huh prompts through the accessible (non-tty) path via the shared
// withStdin helper — the same path a piped installer hits.

func TestSelectIndexAccessible(t *testing.T) {
	withStdin(t, "2\n", func() {
		idx := selectIndex("Pick", []string{"a", "b", "c"}, 2)
		if idx != 1 {
			t.Fatalf("selectIndex = %d, want 1", idx)
		}
	})
}

func TestSelectIndexAccessibleEOFKeepsDefault(t *testing.T) {
	withStdin(t, "", func() {
		idx := selectIndex("Pick", []string{"a", "b", "c"}, 2)
		if idx != 2 {
			t.Fatalf("selectIndex on EOF = %d, want default 2", idx)
		}
	})
}

func TestSecretInputAccessible(t *testing.T) {
	withStdin(t, "sk-test\n", func() {
		got, ok := secretInput("Key", "")
		if !ok || got != "sk-test" {
			t.Fatalf("secretInput = %q, %v; want \"sk-test\", true", got, ok)
		}
	})
}
