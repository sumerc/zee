//go:build integration

package test_test

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zee/clipboard"
)

var testBinary string

func TestMain(m *testing.M) {
	testBinary = os.Getenv("ZEE_TEST_BIN")
	if testBinary == "" {
		fmt.Fprintln(os.Stderr, "ZEE_TEST_BIN not set; run: make test-integration")
		os.Exit(1)
	}

	silencePath := filepath.Join("data", "silence.wav")
	if err := generateSilenceWAV(silencePath, 16000, 1.0); err != nil {
		fmt.Fprintf(os.Stderr, "failed to generate silence.wav: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(silencePath)

	os.Exit(m.Run())
}

func generateSilenceWAV(path string, sampleRate int, durationS float64) error {
	const headerSize = 44
	numSamples := int(float64(sampleRate) * durationS)
	dataSize := numSamples * 2

	buf := make([]byte, headerSize+dataSize)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(headerSize-8+dataSize))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], 1) // mono
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(buf[32:34], 2)  // block align
	binary.LittleEndian.PutUint16(buf[34:36], 16) // bits per sample
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))

	return os.WriteFile(path, buf, 0644)
}

func cmds(parts ...string) string {
	return strings.Join(parts, "\n") + "\n"
}

func runZee(t *testing.T, stdin string, args ...string) (logDir string) {
	t.Helper()
	logDir = t.TempDir()
	cmdArgs := append([]string{"-logpath", logDir}, args...)

	cmd := exec.Command(testBinary, cmdArgs...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = os.Environ()

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("zee exited with error: %v\noutput: %s", err, out)
	}
	return logDir
}

func readLog(t *testing.T, logDir, filename string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(logDir, filename))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("failed to read %s: %v", filename, err)
	}
	return string(data)
}

func requireTranscription(t *testing.T, logDir string) string {
	t.Helper()
	text := readLog(t, logDir, "transcribe_log.txt")
	if strings.TrimSpace(text) == "" {
		t.Fatal("transcribe_log.txt is empty, expected transcribed words")
	}
	return text
}

func requireGroqKey(t *testing.T) {
	t.Helper()
	if os.Getenv("GROQ_API_KEY") == "" {
		t.Skip("GROQ_API_KEY not set")
	}
}

func requireDeepgramKey(t *testing.T) {
	t.Helper()
	if os.Getenv("DEEPGRAM_API_KEY") == "" {
		t.Skip("DEEPGRAM_API_KEY not set")
	}
}

// --- Batch tests ---

func TestBatchWords(t *testing.T) {
	requireGroqKey(t)
	logDir := runZee(t, cmds("KEYDOWN", "KEYUP", "WAIT", "QUIT"), "-test", "data/short.wav")
	requireTranscription(t, logDir)
}

func TestBatchConnReuse(t *testing.T) {
	requireGroqKey(t)
	logDir := runZee(t, cmds("KEYDOWN", "KEYUP", "WAIT", "KEYDOWN", "KEYUP", "WAIT", "QUIT"),
		"-test", "data/short.wav")
	diag := readLog(t, logDir, "diagnostics_log.txt")
	if strings.Count(diag, "transcription") < 2 {
		t.Error("expected 2 transcription entries in diagnostics")
	}
	if !strings.Contains(diag, "conn=reused") {
		t.Error("expected conn=reused in diagnostics")
	}
}

func TestBatchNoVoice(t *testing.T) {
	requireGroqKey(t)
	_ = runZee(t, cmds("KEYDOWN", "SLEEP 1500", "KEYUP", "WAIT", "QUIT"), "-test", "data/silence.wav")
}

func TestBatchEarlyKeyup(t *testing.T) {
	requireGroqKey(t)
	logDir := runZee(t, cmds("KEYDOWN", "SLEEP 500", "KEYUP", "WAIT", "QUIT"), "-test", "data/short.wav")
	_ = readLog(t, logDir, "diagnostics_log.txt")
}

// --- Stream tests ---

func TestStreamWords(t *testing.T) {
	requireDeepgramKey(t)
	logDir := runZee(t, cmds("KEYDOWN", "WAIT_AUDIO_DONE", "SLEEP 300", "KEYUP", "WAIT", "QUIT"),
		"-test", "-stream", "data/short.wav")
	requireTranscription(t, logDir)
}

func TestStreamMetrics(t *testing.T) {
	requireDeepgramKey(t)
	logDir := runZee(t, cmds("KEYDOWN", "WAIT_AUDIO_DONE", "SLEEP 300", "KEYUP", "WAIT", "QUIT"),
		"-test", "-stream", "data/short.wav")
	diag := readLog(t, logDir, "diagnostics_log.txt")
	if !strings.Contains(diag, "stream_transcription") {
		t.Error("expected stream_transcription in diagnostics")
	}
	if !strings.Contains(diag, "connect_ms") {
		t.Error("expected connect_ms in stream metrics")
	}
}

func TestStreamKeyupAtBoundary(t *testing.T) {
	requireDeepgramKey(t)
	logDir := runZee(t, cmds("KEYDOWN", "WAIT_AUDIO_DONE", "KEYUP", "WAIT", "QUIT"),
		"-test", "-stream", "data/short.wav")
	_ = readLog(t, logDir, "diagnostics_log.txt")
}

// --- Clipboard tests ---

func TestPaste(t *testing.T) {
	requireGroqKey(t)
	logDir := runZee(t, cmds("KEYDOWN", "KEYUP", "WAIT", "QUIT"), "-test", "data/short.wav")
	requireTranscription(t, logDir)
	clip, err := clipboard.Read()
	if err != nil {
		t.Skip("clipboard not available")
	}
	if strings.TrimSpace(clip) == "" {
		t.Log("Warning: clipboard is empty after paste test")
	}
}

func TestClipboardRestore(t *testing.T) {
	requireGroqKey(t)

	sentinel := fmt.Sprintf("zee-test-sentinel-%d", time.Now().UnixNano())
	if err := clipboard.Copy(sentinel); err != nil {
		t.Skip("clipboard not available")
	}

	_ = runZee(t, cmds("KEYDOWN", "KEYUP", "WAIT", "SLEEP 1200", "QUIT"), "-test", "data/short.wav")

	clip, err := clipboard.Read()
	if err != nil {
		t.Skip("clipboard not available")
	}
	if strings.TrimSpace(clip) != sentinel {
		t.Errorf("clipboard not restored: got %q, want %q", strings.TrimSpace(clip), sentinel)
	}
}
