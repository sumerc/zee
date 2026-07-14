//go:build integration

package test_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode"

	"zee/clipboard"
	"zee/localmodel"
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

// seedCredentials writes a per-run credentials.json into a fresh config dir for
// any *_API_KEY present in env (later entries win, matching how opts.env is
// appended after os.Environ), and returns that dir for ZEE_CONFIG_DIR. The app
// no longer reads keys from the environment, so this bridges CI key secrets to
// the subprocess while isolating each run's config.
func seedCredentials(t *testing.T, env []string) string {
	t.Helper()
	envToProvider := map[string]string{
		"GROQ_API_KEY":       "groq",
		"DEEPGRAM_API_KEY":   "deepgram",
		"OPENAI_API_KEY":     "openai",
		"MISTRAL_API_KEY":    "mistral",
		"ELEVENLABS_API_KEY": "elevenlabs",
	}
	creds := map[string]string{}
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		p, ok := envToProvider[kv[:i]]
		if !ok {
			continue
		}
		if v := kv[i+1:]; v != "" {
			creds[p] = v
		} else {
			delete(creds, p)
		}
	}
	dir := t.TempDir()
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		t.Fatalf("marshal credentials: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), data, 0600); err != nil {
		t.Fatalf("seed credentials: %v", err)
	}
	return dir
}

type runOpts struct {
	env     []string // extra KEY=VALUE pairs
	wantErr bool     // expect non-zero exit
}

func runZee(t *testing.T, stdin string, args ...string) (logDir string) {
	t.Helper()
	return runZeeOpts(t, stdin, runOpts{}, args...)
}

func runZeeOpts(t *testing.T, stdin string, opts runOpts, args ...string) (logDir string) {
	t.Helper()
	logDir, _ = runZeeDirs(t, stdin, opts, args...)
	return logDir
}

// runZeeDirs also returns the run's isolated config dir, for tests that assert
// on config-side artifacts (samples/, credentials.json, config.json).
func runZeeDirs(t *testing.T, stdin string, opts runOpts, args ...string) (logDir, cfgDir string) {
	t.Helper()
	logDir = t.TempDir()
	cmdArgs := append([]string{"-logpath", logDir, "-debug-transcribe"}, args...)

	cmd := exec.Command(testBinary, cmdArgs...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), opts.env...)
	// Keys come from credentials.json now, not the environment. Bridge any
	// *_API_KEY in the effective env into a fresh per-run credentials.json (also
	// isolating config), so CI key secrets and the "empty key" overrides still
	// reach the subprocess with identical provider-resolution behavior.
	cfgDir = seedCredentials(t, cmd.Env)
	cmd.Env = append(cmd.Env, "ZEE_CONFIG_DIR="+cfgDir)

	out, err := cmd.CombinedOutput()
	// If the test later fails an assertion, the evidence (zee's own output and
	// the diagnostics log) would vanish with the temp dir — dump both so a CI
	// failure log is enough to diagnose what actually happened.
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		t.Logf("zee args: %q", cmdArgs)
		t.Logf("zee combined output:\n%s", out)
		for _, f := range []string{"diagnostics_log.txt", "transcribe_log.txt"} {
			if data, err := os.ReadFile(filepath.Join(logDir, f)); err == nil {
				t.Logf("%s:\n%s", f, data)
			} else {
				t.Logf("%s: %v", f, err)
			}
		}
	})
	if opts.wantErr {
		if err == nil {
			t.Fatalf("expected zee to exit with error, but it succeeded\noutput: %s", out)
		}
		return logDir, cfgDir
	}
	if err != nil {
		t.Fatalf("zee exited with error: %v\noutput: %s", err, out)
	}
	return logDir, cfgDir
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
		t.Log("warning: expected conn=reused in diagnostics (server may have closed idle connection)")
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
		"-test", "data/short.wav")
	requireTranscription(t, logDir)
}

func TestStreamMetrics(t *testing.T) {
	requireDeepgramKey(t)
	logDir := runZee(t, cmds("KEYDOWN", "WAIT_AUDIO_DONE", "SLEEP 300", "KEYUP", "WAIT", "QUIT"),
		"-test", "data/short.wav")
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
		"-test", "data/short.wav")
	_ = readLog(t, logDir, "diagnostics_log.txt")
}

// --- Failure recovery: a failed transcription must auto-save the recording ---

// requireSavedSample returns the single auto-saved sample under <cfg>/samples
// and its parsed info.json. These tests use deliberately invalid API keys, so
// they need no secrets and never reach the real provider.
func requireSavedSample(t *testing.T, cfgDir string) (dir string, info map[string]string) {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(cfgDir, "samples", "*", "info.json"))
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 auto-saved sample under %s/samples, found %d", cfgDir, len(matches))
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read info.json: %v", err)
	}
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("parse info.json: %v", err)
	}
	return filepath.Dir(matches[0]), info
}

func TestBatchFailureAutoSavesSample(t *testing.T) {
	_, cfgDir := runZeeDirs(t, cmds("KEYDOWN", "KEYUP", "WAIT", "QUIT"),
		runOpts{env: []string{"GROQ_API_KEY=zee-invalid-key"}},
		"-test", "data/short.wav", "-provider", "groq")
	dir, info := requireSavedSample(t, cfgDir)
	if info["error"] == "" {
		t.Error("info.json error field is empty, expected the provider failure")
	}
	audio := filepath.Join(dir, "audio."+info["format"])
	if st, err := os.Stat(audio); err != nil || st.Size() == 0 {
		t.Errorf("saved audio missing or empty (%s): %v", audio, err)
	}
}

func TestStreamFailureAutoSavesSample(t *testing.T) {
	// The websocket handshake fails (bad key), yet the dictation audio must be
	// retained and auto-saved as WAV — stream sessions used to lose it entirely.
	_, cfgDir := runZeeDirs(t, cmds("KEYDOWN", "WAIT_AUDIO_DONE", "SLEEP 300", "KEYUP", "WAIT", "QUIT"),
		runOpts{env: []string{"DEEPGRAM_API_KEY=zee-invalid-key"}},
		"-test", "data/short.wav", "-provider", "deepgram")
	dir, info := requireSavedSample(t, cfgDir)
	if info["error"] == "" {
		t.Error("info.json error field is empty, expected the connect failure")
	}
	if info["format"] != "wav" {
		t.Errorf("format = %q, want wav (retained stream PCM)", info["format"])
	}
	if st, err := os.Stat(filepath.Join(dir, "audio.wav")); err != nil || st.Size() <= 44 {
		t.Errorf("saved audio.wav missing or no PCM beyond the header: %v", err)
	}
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

// --- Silence detection tests (no API key needed) ---

// silenceWarnSleep must exceed silenceWarnEvery (8s) in silence.go
const silenceWarnSleep = "SLEEP 9000"

func TestNoVoiceWarningBatch(t *testing.T) {
	logDir := runZeeOpts(t, cmds("KEYDOWN", silenceWarnSleep, "KEYUP", "WAIT", "QUIT"),
		runOpts{env: []string{"ZEE_FAKE_TEXT=hello", "GROQ_API_KEY=", "DEEPGRAM_API_KEY="}},
		"-test", "data/silence.wav")
	diag := readLog(t, logDir, "diagnostics_log.txt")
	if !strings.Contains(diag, "no_voice_warning") {
		t.Errorf("expected 'no_voice_warning' in diagnostics, got: %q", diag)
	}
}

func TestNoVoiceWarningStream(t *testing.T) {
	logDir := runZeeOpts(t, cmds("KEYDOWN", silenceWarnSleep, "KEYUP", "WAIT", "QUIT"),
		runOpts{env: []string{"ZEE_FAKE_TEXT=hello", "ZEE_FAKE_STREAM=1", "GROQ_API_KEY=", "DEEPGRAM_API_KEY="}},
		"-test", "data/silence.wav")
	diag := readLog(t, logDir, "diagnostics_log.txt")
	if !strings.Contains(diag, "no_voice_warning") {
		t.Errorf("expected 'no_voice_warning' in diagnostics, got: %q", diag)
	}
}

// --- Fake transcriber tests (no API key needed) ---

func TestFakeTranscriberWords(t *testing.T) {
	logDir := runZeeOpts(t, cmds("KEYDOWN", "KEYUP", "WAIT", "QUIT"),
		runOpts{env: []string{"ZEE_FAKE_TEXT=hello world", "GROQ_API_KEY=", "DEEPGRAM_API_KEY="}},
		"-test", "data/short.wav")
	text := readLog(t, logDir, "transcribe_log.txt")
	if !strings.Contains(text, "hello world") {
		t.Errorf("expected 'hello world' in transcribe log, got: %q", text)
	}
}

func TestFakeTranscriberError(t *testing.T) {
	logDir := runZeeOpts(t, cmds("KEYDOWN", "KEYUP", "WAIT", "QUIT"),
		runOpts{env: []string{"ZEE_FAKE_TEXT=test", "ZEE_FAKE_ERROR=1", "GROQ_API_KEY=", "DEEPGRAM_API_KEY="}},
		"-test", "data/short.wav")
	diag := readLog(t, logDir, "diagnostics_log.txt")
	if !strings.Contains(diag, "fake transcriber error") {
		t.Errorf("expected 'fake transcriber error' in diagnostics, got: %q", diag)
	}
}

func TestClipboardRestoreOnError(t *testing.T) {
	sentinel := fmt.Sprintf("zee-test-sentinel-%d", time.Now().UnixNano())
	if err := clipboard.Copy(sentinel); err != nil {
		t.Skip("clipboard not available")
	}

	_ = runZeeOpts(t, cmds("KEYDOWN", "KEYUP", "WAIT", "SLEEP 1200", "QUIT"),
		runOpts{env: []string{"ZEE_FAKE_TEXT=test", "ZEE_FAKE_ERROR=1", "GROQ_API_KEY=", "DEEPGRAM_API_KEY="}},
		"-test", "data/short.wav")

	clip, err := clipboard.Read()
	if err != nil {
		t.Skip("clipboard not available")
	}
	if strings.TrimSpace(clip) != sentinel {
		t.Errorf("clipboard not restored on error: got %q, want %q", strings.TrimSpace(clip), sentinel)
	}
}

// --- Local model (Parakeet) tests ---
//
// End-to-end check that the on-device models transcribe their own languages:
// the default English 110m, and the multilingual v3 across English, French and
// Russian (auto-detect). Audio fixtures are committed WAVs synthesized with
// macOS `say` so the expected transcript is known. Each case self-skips when
// its gguf isn't downloaded (run `make download-models`), so the suite stays
// green on machines/CI without the local models.

// localModelsDir is the dev gguf location relative to the test working dir
// (<repo>/test): models live at <repo>/models/parakeet/<Version>.
func localModelsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "models", "parakeet", localmodel.Version))
	if err != nil {
		t.Fatalf("resolve models dir: %v", err)
	}
	return dir
}

// transcribeFiles runs `zee -transcribe` over one or more files in a SINGLE
// process (the model is loaded once and reused across files) and returns the
// per-file transcripts — one per stdout line, in input order. Skips if the
// model's gguf isn't present.
func transcribeFiles(t *testing.T, modelID, lang string, files ...string) []string {
	t.Helper()
	m, ok := localmodel.ByID(modelID)
	if !ok {
		t.Fatalf("unknown local model %q", modelID)
	}
	modelsDir := localModelsDir(t)
	if fi, err := os.Stat(filepath.Join(modelsDir, m.Filename)); err != nil || fi.Size() != m.SizeBytes {
		t.Skipf("model %q not downloaded (run: make download-models)", modelID)
	}

	// Flags must precede the positional files: Go's flag parser stops at the
	// first non-flag arg, so -transcribe and the files come last.
	args := append([]string{"-logpath", t.TempDir(), "-provider", "parakeet",
		"-model", modelID, "-lang", lang, "-transcribe"}, files...)
	cmd := exec.Command(testBinary, args...)
	cmd.Env = append(os.Environ(), "ZEE_MODELS_DIR="+modelsDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	start := time.Now()
	if err := cmd.Run(); err != nil {
		t.Fatalf("transcribe %v failed: %v\nstderr: %s", files, err, stderr.String())
	}
	t.Logf("%s: %d file(s) in %s", modelID, len(files), time.Since(start).Round(time.Millisecond))

	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != len(files) {
		t.Fatalf("expected %d transcripts, got %d:\n%s", len(files), len(lines), stdout.String())
	}
	return lines
}

// assertTranscript checks the transcript matches the expected text by normalized
// token overlap (TTS+ASR drifts slightly, so we don't require an exact match).
func assertTranscript(t *testing.T, got, want string) {
	t.Helper()
	g, w := normalizeText(got), normalizeText(want)
	if o := tokenOverlap(g, w); o < 0.8 {
		t.Errorf("token overlap %.2f below 0.8\n got:  %q\n want: %q", o, g, w)
	}
}

// normalizeText lowercases and collapses everything but letters/digits to single
// spaces, so punctuation and casing don't fail the comparison.
func normalizeText(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// tokenOverlap is the fraction of want's tokens present in got (multiset). TTS+
// ASR can drift slightly, so we assert a high overlap rather than exact match.
func tokenOverlap(got, want string) float64 {
	w := strings.Fields(want)
	if len(w) == 0 {
		return 0
	}
	have := map[string]int{}
	for _, tok := range strings.Fields(got) {
		have[tok]++
	}
	hits := 0
	for _, tok := range w {
		if have[tok] > 0 {
			have[tok]--
			hits++
		}
	}
	return float64(hits) / float64(len(w))
}

func TestLocalParakeetModels(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("local Parakeet transcription is darwin/arm64 only")
	}

	const wantEN = "The quick brown fox jumps."

	t.Run("english-110m", func(t *testing.T) {
		got := transcribeFiles(t, "parakeet-110m-en", "en", "data/en.wav")
		assertTranscript(t, got[0], wantEN)
	})

	// One process, one v3 load, three languages (auto-detect).
	t.Run("v3-multilingual", func(t *testing.T) {
		got := transcribeFiles(t, "parakeet-v3-multi", "",
			"data/en.wav", "data/fr.wav", "data/ru.wav")
		assertTranscript(t, got[0], wantEN)
		assertTranscript(t, got[1], "Je m'appelle Thomas Dupont.")
		assertTranscript(t, got[2], "Меня зовут Милена Иванова.")
	})
}

// TestLocalModelDiagnostics checks that a local-model transcription emits the
// same diagnostics/metrics record as the cloud path. We assert on the presence
// of stable markers (provider, and a few metric keys) rather than parsing the
// line, so the log format can evolve without breaking the test. The recording
// path (-test) is what logs metrics; -transcribe is a quiet one-shot.
func TestLocalModelDiagnostics(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("local Parakeet transcription is darwin/arm64 only")
	}
	const modelID = "parakeet-v3-multi"
	m, ok := localmodel.ByID(modelID)
	if !ok {
		t.Fatalf("unknown local model %q", modelID)
	}
	modelsDir := localModelsDir(t)
	if fi, err := os.Stat(filepath.Join(modelsDir, m.Filename)); err != nil || fi.Size() != m.SizeBytes {
		t.Skipf("model %q not downloaded (run: make download-models)", modelID)
	}

	// Flags must precede the positional WAV: Go's flag parsing stops at the
	// first non-flag argument. runZeeOpts already orders them correctly.
	logDir := runZeeOpts(t, cmds("KEYDOWN", "KEYUP", "WAIT", "SLEEP 500", "QUIT"),
		runOpts{env: []string{"ZEE_MODELS_DIR=" + modelsDir}},
		"-provider", "parakeet", "-model", modelID, "-lang", "", "-test", "data/fr.wav")

	diag := readLog(t, logDir, "diagnostics_log.txt")
	for _, marker := range []string{"transcription", "provider=parakeet", "inference_ms", "rss_mb", "audio_s"} {
		if !strings.Contains(diag, marker) {
			t.Errorf("diagnostics missing %q marker\n--- diagnostics ---\n%s", marker, diag)
		}
	}
}
