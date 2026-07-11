package setup

import (
	"os"
	"testing"

	"zee/config"
	"zee/hotkey"
	"zee/transcriber"
)

// withStdin redirects os.Stdin to a pipe carrying input for the duration of fn.
func withStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	go func() { w.WriteString(input); w.Close() }()
	defer func() { os.Stdin = old; r.Close() }()
	fn()
}

// TestReadLineNoSwallow guards the fix for the input-buffering bug: each readLine
// must consume exactly one line, leaving the rest for the next prompt (a shared
// bufio.Reader used to swallow everything into its buffer, so later prompts got
// EOF and silently took defaults).
func TestReadLineNoSwallow(t *testing.T) {
	withStdin(t, "one\ntwo\nthree\n", func() {
		for _, want := range []string{"one", "two", "three"} {
			if got := readLine(); got != want {
				t.Fatalf("readLine() = %q, want %q (bytes were swallowed?)", got, want)
			}
		}
	})
}

// TestNumberedMenuChoice exercises the non-tty fallback (piped stdin can't enter
// raw mode) and confirms a numeric choice maps to the right index.
func TestNumberedMenuChoice(t *testing.T) {
	opts := []string{"a", "b", "c"}
	withStdin(t, "3\n", func() {
		if got := menu("pick", opts, 0); got != 2 {
			t.Fatalf("menu choice = %d, want 2", got)
		}
	})
	withStdin(t, "\n", func() { // Enter keeps the start default
		if got := menu("pick", opts, 1); got != 1 {
			t.Fatalf("menu default = %d, want 1", got)
		}
	})
}

func TestCurrentComboDefaultsWhenUnset(t *testing.T) {
	config.SetDir(t.TempDir())
	if err := config.Load(); err != nil {
		t.Fatal(err)
	}
	got := currentCombo()
	if !comboEqual(got, hotkey.DefaultCombo()) {
		t.Fatalf("currentCombo() = %+v, want default %+v", got, hotkey.DefaultCombo())
	}
}

func TestCurrentComboUsesSaved(t *testing.T) {
	config.SetDir(t.TempDir())
	if err := config.Load(); err != nil {
		t.Fatal(err)
	}
	config.Update(func(s *config.Settings) {
		s.Hotkey = config.Hotkey{Mods: []string{"option"}, Key: 49, Label: "⌥Space"}
	})
	got := currentCombo()
	if got.Label != "⌥Space" || got.Key != 49 || len(got.Mods) != 1 || got.Mods[0] != "option" {
		t.Fatalf("currentCombo() = %+v, want the saved ⌥Space combo", got)
	}
}

func TestProviderReady(t *testing.T) {
	config.SetDir(t.TempDir())
	if err := config.Load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { transcriber.SetKeySource(func(string) string { return "" }) })

	// Cloud provider selected but no key → not ready.
	config.Update(func(s *config.Settings) { s.Provider = "groq" })
	transcriber.SetKeySource(func(string) string { return "" })
	if ok, detail := providerReady(); ok {
		t.Errorf("groq without key: ready=true (detail %q), want false", detail)
	}

	// Key present via the injected source → ready, labelled Groq.
	transcriber.SetKeySource(func(p string) string {
		if p == "groq" {
			return "gsk_x"
		}
		return ""
	})
	ok, detail := providerReady()
	if !ok || detail != "Groq" {
		t.Errorf("groq with key: ready=%v detail=%q, want true/\"Groq\"", ok, detail)
	}
}

func TestBoolWord(t *testing.T) {
	if got := boolWord(true, "yes", "no"); got != "yes" {
		t.Errorf("boolWord(true) = %q", got)
	}
	if got := boolWord(false, "yes", "no"); got != "no" {
		t.Errorf("boolWord(false) = %q", got)
	}
}

func comboEqual(a, b hotkey.Combo) bool {
	if a.Key != b.Key || a.Label != b.Label || len(a.Mods) != len(b.Mods) {
		return false
	}
	for i := range a.Mods {
		if a.Mods[i] != b.Mods[i] {
			return false
		}
	}
	return true
}
