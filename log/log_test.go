package log

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupLogDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	SetDir(tmp)
	t.Cleanup(func() { Close(); SetDir("") })
	return tmp
}

func TestResolveDirFlag(t *testing.T) {
	got, err := ResolveDir("/tmp/mylog")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/mylog" {
		t.Errorf("got %q, want /tmp/mylog", got)
	}
}

func TestResolveDirFlagRelative(t *testing.T) {
	got, err := ResolveDir("logs")
	if err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(wd, "logs")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveDirEnv(t *testing.T) {
	t.Setenv("ZEE_LOG_PATH", "/tmp/zee-env-log")
	got, err := ResolveDir("")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/zee-env-log" {
		t.Errorf("got %q, want /tmp/zee-env-log", got)
	}
}

func TestResolveDirDefault(t *testing.T) {
	t.Setenv("ZEE_LOG_PATH", "")
	got, err := ResolveDir("")
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Error("expected non-empty default directory")
	}
}

func TestInitCreatesFiles(t *testing.T) {
	tmp := setupLogDir(t)

	if err := Init(); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"diagnostics_log.txt", "transcribe_log.txt"} {
		path := filepath.Join(tmp, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s not created: %v", name, err)
		}
	}
}

func TestTranscriptionText(t *testing.T) {
	tmp := setupLogDir(t)

	if err := Init(); err != nil {
		t.Fatal(err)
	}

	TranscriptionText("hello world")

	data, err := os.ReadFile(filepath.Join(tmp, "transcribe_log.txt"))
	if err != nil {
		t.Fatal(err)
	}
	line := string(data)
	if !strings.Contains(line, "hello world") {
		t.Errorf("transcribe_log.txt missing text, got: %q", line)
	}
	// format: "2006-01-02 15:04:05\t[pid]\ttext\n"
	if !strings.Contains(line, "\t") {
		t.Errorf("expected tab-separated format, got: %q", line)
	}
}

func TestCloseIdempotent(t *testing.T) {
	setupLogDir(t)

	if err := Init(); err != nil {
		t.Fatal(err)
	}
	Close()
	Close() // should not panic
}
