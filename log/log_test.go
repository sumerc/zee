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
	SetTranscribeEnabled(true)
	t.Cleanup(func() { SetTranscribeEnabled(false) })

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

func TestInitWithoutTranscribe(t *testing.T) {
	tmp := setupLogDir(t)

	if err := Init(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(tmp, "diagnostics_log.txt")); err != nil {
		t.Errorf("diagnostics_log.txt not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "transcribe_log.txt")); err == nil {
		t.Error("transcribe_log.txt should not be created when transcribe is disabled")
	}
}

func TestTranscriptionText(t *testing.T) {
	tmp := setupLogDir(t)
	SetTranscribeEnabled(true)
	t.Cleanup(func() { SetTranscribeEnabled(false) })

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

// TestInitRotatesOversizedLog: logging is always on, so Init must rotate an
// over-cap diagnostics log to .old instead of appending forever.
func TestInitRotatesOversizedLog(t *testing.T) {
	dir := setupLogDir(t)

	path := filepath.Join(dir, "diagnostics_log.txt")
	if err := os.WriteFile(path, make([]byte, maxLogSize+1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	Info("fresh line")

	if st, err := os.Stat(path + ".old"); err != nil || st.Size() != maxLogSize+1 {
		t.Errorf("expected rotated .old with original size, got err=%v", err)
	}
	if st, err := os.Stat(path); err != nil || st.Size() > 1024 {
		t.Errorf("expected fresh small log after rotation, size=%v err=%v", st.Size(), err)
	}
}

// TestInitKeepsSmallLog: under the cap the file must be appended, not rotated.
func TestInitKeepsSmallLog(t *testing.T) {
	dir := setupLogDir(t)

	path := filepath.Join(dir, "diagnostics_log.txt")
	if err := os.WriteFile(path, []byte("existing line\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	Info("appended line")
	Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "existing line") || !strings.Contains(string(data), "appended line") {
		t.Errorf("expected append below existing content, got: %q", data)
	}
	if _, err := os.Stat(path + ".old"); !os.IsNotExist(err) {
		t.Error("small log must not be rotated")
	}
}
