package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAPIKeyAbsentFile(t *testing.T) {
	SetDir(t.TempDir())
	if got := APIKey("groq"); got != "" {
		t.Fatalf("APIKey with no file = %q, want \"\"", got)
	}
	if HasAPIKey("groq") {
		t.Fatal("HasAPIKey = true with no file")
	}
}

func TestSetAPIKeyRoundTrip(t *testing.T) {
	SetDir(t.TempDir())

	if err := SetAPIKey("groq", "gsk_test"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}
	if got := APIKey("groq"); got != "gsk_test" {
		t.Fatalf("APIKey = %q, want %q", got, "gsk_test")
	}
	if !HasAPIKey("groq") {
		t.Fatal("HasAPIKey = false after set")
	}
}

func TestSetAPIKeyPreservesOthers(t *testing.T) {
	SetDir(t.TempDir())

	if err := SetAPIKey("groq", "gsk_a"); err != nil {
		t.Fatal(err)
	}
	if err := SetAPIKey("openai", "sk_b"); err != nil {
		t.Fatal(err)
	}
	if got := APIKey("groq"); got != "gsk_a" {
		t.Fatalf("groq key clobbered: %q", got)
	}
	if got := APIKey("openai"); got != "sk_b" {
		t.Fatalf("openai key = %q, want sk_b", got)
	}
}

func TestSetAPIKeyRemove(t *testing.T) {
	SetDir(t.TempDir())

	SetAPIKey("groq", "gsk_a")
	if err := SetAPIKey("groq", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if HasAPIKey("groq") {
		t.Fatal("key still present after clearing")
	}
}

func TestCredentialsFilePerms(t *testing.T) {
	d := t.TempDir()
	SetDir(d)

	if err := SetAPIKey("groq", "gsk_secret"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(d, "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0600 {
		t.Fatalf("credentials.json perms = %o, want 0600", perm)
	}
}

func TestCredentialsCorruptFile(t *testing.T) {
	d := t.TempDir()
	SetDir(d)

	os.WriteFile(filepath.Join(d, "credentials.json"), []byte("not json{{{"), 0600)
	if got := APIKey("groq"); got != "" {
		t.Fatalf("corrupt file should yield \"\", got %q", got)
	}
	// A subsequent write must still succeed (overwriting the corrupt file).
	if err := SetAPIKey("groq", "gsk_ok"); err != nil {
		t.Fatalf("SetAPIKey over corrupt file: %v", err)
	}
	if got := APIKey("groq"); got != "gsk_ok" {
		t.Fatalf("APIKey after recovery = %q", got)
	}
}
