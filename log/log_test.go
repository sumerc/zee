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

func TestTranscription(t *testing.T) {
	tmp := setupLogDir(t)
	SetTranscribeEnabled(true)
	t.Cleanup(func() { SetTranscribeEnabled(false) })

	if err := Init(); err != nil {
		t.Fatal(err)
	}

	Transcription(Recording{Text: "hello world", Audio: []byte("RIFF"), AudioExt: "wav", Provider: "parakeet"})
	Transcription(Recording{Text: "no audio"})

	data, err := os.ReadFile(filepath.Join(tmp, "transcribe_log.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %q", data)
	}

	// format: "2006-01-02 15:04:05\t[pid]\t<recording dir or ->\ttext"
	f := strings.Split(lines[0], "\t")
	if len(f) != 4 || f[3] != "hello world" {
		t.Fatalf("unexpected line: %q", lines[0])
	}
	rec := filepath.Join(tmp, f[2])
	for name, want := range map[string]string{"audio.wav": "RIFF", "transcript.txt": "hello world\n"} {
		got, err := os.ReadFile(filepath.Join(rec, name))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	if info, err := os.ReadFile(filepath.Join(rec, "info.json")); err != nil || !strings.Contains(string(info), `"parakeet"`) {
		t.Errorf("info.json = %q, %v", info, err)
	}

	if f := strings.Split(lines[1], "\t"); len(f) != 4 || f[2] != "-" {
		t.Errorf("line without audio should carry \"-\" as its path: %q", lines[1])
	}
	if _, err := os.Stat(filepath.Join(rec, "post_transcript.txt")); err == nil {
		t.Error("post_transcript.txt written although post-transcribe did not run")
	}
}

func TestTranscriptionPostTranscribe(t *testing.T) {
	tmp := setupLogDir(t)
	SetTranscribeEnabled(true)
	t.Cleanup(func() { SetTranscribeEnabled(false) })

	if err := Init(); err != nil {
		t.Fatal(err)
	}

	post := PostTranscript{Profile: "friendly", Model: "m", Text: "Hello there!"}
	Transcription(Recording{Text: "hello their", Audio: []byte("RIFF"), AudioExt: "wav", Post: post})
	Transcription(Recording{Text: "raw", Audio: []byte("RIFF"), AudioExt: "wav",
		Post: PostTranscript{Profile: "friendly", Model: "m", Error: "timeout"}})

	data, err := os.ReadFile(filepath.Join(tmp, "transcribe_log.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %q", data)
	}

	// The rewrite follows the raw text as a fifth field.
	f := strings.Split(lines[0], "\t")
	if len(f) != 5 || f[3] != "hello their" || f[4] != "Hello there!" {
		t.Fatalf("unexpected line: %q", lines[0])
	}
	rec := filepath.Join(tmp, f[2])
	for name, want := range map[string]string{"transcript.txt": "hello their\n", "post_transcript.txt": "Hello there!\n"} {
		if got, err := os.ReadFile(filepath.Join(rec, name)); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	if info, _ := os.ReadFile(filepath.Join(rec, "info.json")); !strings.Contains(string(info), `"profile": "friendly"`) {
		t.Errorf("info.json lacks the post_transcribe profile: %s", info)
	}

	// A failed rewrite keeps the line at four fields and records the error.
	f = strings.Split(lines[1], "\t")
	if len(f) != 4 {
		t.Fatalf("unexpected line: %q", lines[1])
	}
	rec = filepath.Join(tmp, f[2])
	if _, err := os.Stat(filepath.Join(rec, "post_transcript.txt")); err == nil {
		t.Error("post_transcript.txt written for a failed rewrite")
	}
	if info, _ := os.ReadFile(filepath.Join(rec, "info.json")); !strings.Contains(string(info), `"error": "timeout"`) {
		t.Errorf("info.json lacks the post_transcribe error: %s", info)
	}
}

// TestTranscriptionPostTranscribeMultiline: a raw transcript and a rewrite with
// line breaks and tabs stay on one log line, escaped; transcript.txt and
// post_transcript.txt keep them as is.
func TestTranscriptionPostTranscribeMultiline(t *testing.T) {
	tmp := setupLogDir(t)
	SetTranscribeEnabled(true)
	t.Cleanup(func() { SetTranscribeEnabled(false) })

	if err := Init(); err != nil {
		t.Fatal(err)
	}

	raw := "hi\tcould you\ncheck"
	rewrite := "Hi,\r\n\nCould you\tcheck C:\\new?"
	Transcription(Recording{Text: raw, Audio: []byte("RIFF"), AudioExt: "wav",
		Post: PostTranscript{Profile: "friendly", Model: "m", Text: rewrite}})

	data, err := os.ReadFile(filepath.Join(tmp, "transcribe_log.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %q", data)
	}
	f := strings.Split(lines[0], "\t")
	if want := `Hi,\r\n\nCould you\tcheck C:\\new?`; len(f) != 5 || f[3] != `hi\tcould you\ncheck` || f[4] != want {
		t.Fatalf("unexpected line: %q, want last field %q", lines[0], want)
	}
	for name, want := range map[string]string{"transcript.txt": raw + "\n", "post_transcript.txt": rewrite + "\n"} {
		if got, _ := os.ReadFile(filepath.Join(tmp, f[2], name)); string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
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
