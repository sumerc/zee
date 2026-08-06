package log

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

var (
	diagLog        zerolog.Logger
	diagFile       *os.File
	transcribeFile *os.File
	logMu          sync.Mutex
	logReady       atomic.Bool
	transcribeOn   bool
	pid            int
	dir            string
)

type Metrics struct {
	AudioLengthS     float64
	RawSizeKB        float64
	CompressedSizeKB float64
	CompressionPct   float64
	EncodeTimeMs     float64
	DNSTimeMs        float64
	TLSTimeMs        float64
	TTFBMs           float64
	TotalTimeMs      float64
	ProcessRSSMB     float64
	InferenceMs      float64
	PressToRecordMs  float64 // press → mic-live; a stall here (fork/lock) misses quick taps
}

func ResolveDir(flagPath string) (string, error) {
	// Priority 1: -logpath flag
	if flagPath != "" {
		if !filepath.IsAbs(flagPath) {
			wd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			return filepath.Join(wd, flagPath), nil
		}
		return flagPath, nil
	}

	// Priority 2: ZEE_LOG_PATH environment variable
	envPath := os.Getenv("ZEE_LOG_PATH")
	if envPath != "" {
		if !filepath.IsAbs(envPath) {
			wd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			return filepath.Join(wd, envPath), nil
		}
		return envPath, nil
	}

	// Priority 3: Default OS-specific location
	return getDefaultDir()
}

func SetDir(d string) {
	dir = d
}

func Dir() string {
	return dir
}

func EnsureDir() error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}
	return nil
}

func SetTranscribeEnabled(on bool) {
	logMu.Lock()
	transcribeOn = on
	logMu.Unlock()
}

// maxLogSize caps each append-only log file: logging is always on, so without
// a bound the files would grow for the life of the install. On overflow the
// file is rotated to <name>.old (one generation kept).
const maxLogSize = 10 << 20 // 10 MB

func rotateIfLarge(path string) {
	if st, err := os.Stat(path); err == nil && st.Size() > maxLogSize {
		os.Remove(path + ".old") // Windows: Rename won't replace an existing file
		os.Rename(path, path+".old")
	}
}

func Init() error {
	logMu.Lock()
	defer logMu.Unlock()

	if err := EnsureDir(); err != nil {
		return err
	}

	pid = os.Getpid()

	var err error

	diagPath := filepath.Join(dir, "diagnostics_log.txt")
	rotateIfLarge(diagPath)
	diagFile, err = os.OpenFile(diagPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	if transcribeOn {
		transcribePath := filepath.Join(dir, "transcribe_log.txt")
		rotateIfLarge(transcribePath)
		transcribeFile, err = os.OpenFile(transcribePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			diagFile.Close()
			return err
		}
	}

	consoleWriter := zerolog.ConsoleWriter{
		Out:        diagFile,
		TimeFormat: "2006-01-02 15:04:05",
		NoColor:    true,
	}
	diagLog = zerolog.New(consoleWriter).With().Timestamp().Int("pid", pid).Logger()

	logReady.Store(true)
	return nil
}

func Close() {
	logMu.Lock()
	defer logMu.Unlock()
	if diagFile != nil {
		diagFile.Close()
		diagFile = nil
	}
	if transcribeFile != nil {
		transcribeFile.Close()
		transcribeFile = nil
	}
	logReady.Store(false)
}

func Info(msg string) {
	if logReady.Load() {
		diagLog.Info().Msg(msg)
	}
}

func Error(msg string) {
	if logReady.Load() {
		diagLog.Error().Msg(msg)
	}
}

func Errorf(format string, args ...any) {
	if logReady.Load() {
		diagLog.Error().Msgf(format, args...)
	}
}

func Warn(msg string) {
	if logReady.Load() {
		diagLog.Warn().Msg(msg)
	}
}

func Warnf(format string, args ...any) {
	if logReady.Load() {
		diagLog.Warn().Msgf(format, args...)
	}
}

func TranscriptionMetrics(m Metrics, mode, format, provider string, connReused bool, tlsProto string) {
	if !logReady.Load() {
		return
	}

	connStatus := "new"
	if connReused {
		connStatus = "reused"
	}

	ev := diagLog.Info().
		Str("mode", mode).
		Str("format", format).
		Str("provider", provider).
		Str("conn", connStatus)
	if tlsProto != "" {
		ev = ev.Str("tls_proto", tlsProto)
	}
	ev.Float64("audio_s", m.AudioLengthS).
		Float64("raw_kb", m.RawSizeKB).
		Float64("compressed_kb", m.CompressedSizeKB).
		Float64("compression_pct", m.CompressionPct).
		Float64("encode_ms", m.EncodeTimeMs).
		Float64("dns_ms", m.DNSTimeMs).
		Float64("tls_ms", m.TLSTimeMs).
		Float64("ttfb_ms", m.TTFBMs).
		Float64("total_ms", m.TotalTimeMs).
		Float64("rss_mb", m.ProcessRSSMB)
	if m.InferenceMs > 0 {
		ev = ev.Float64("inference_ms", m.InferenceMs)
	}
	if m.PressToRecordMs > 0 {
		ev = ev.Float64("press_to_record_ms", m.PressToRecordMs)
	}
	ev.Msg("transcription")
}

// HotkeyPress records one push-to-talk press: how long the keys were held as
// the app *observed* them (down_to_up_ms — a value far above the tap/hold
// threshold on a quick tap means the keyup event was delivered late, i.e. the
// run loop stalled) and how it resolved (hold/toggle/denied).
func HotkeyPress(downToUpMs float64, mode string) {
	if !logReady.Load() {
		return
	}
	diagLog.Info().Float64("down_to_up_ms", downToUpMs).Str("mode", mode).Msg("hotkey_press")
}

// LatencyBreakdown itemizes the release→text window so a slow felt_latency line
// decomposes into its stages. ClipSaveMs is the pbpaste fork's own duration; it
// runs concurrently with inference, so only ClipWaitMs — how long the finish
// path actually blocked waiting for it — belongs to the serial sum. Zero fields
// are stages that didn't run (stream path, autoPaste off) and are omitted.
type LatencyBreakdown struct {
	TailWaitMs  float64 // configured mic tail-wait after release
	MicStopMs   float64 // capture device stop + callback clear
	ConvertMs   float64 // local path: PCM→f32 + PCM→WAV before inference
	InferenceMs float64 // engine/provider time (repeated from the transcription line)
	ClipSaveMs  float64 // pbpaste fork, concurrent with inference — informational
	ClipWaitMs  float64 // block on the pbpaste fork after inference returned
	PasteCopyMs float64 // pbcopy fork inside PasteText
	PasteKeyMs  float64 // Cmd+V keystroke synthesis inside PasteText
}

// ReleaseToText records the one latency the user actually feels: hotkey release
// (or silence auto-close) → text delivered to the clipboard/paste. It spans the
// whole tail — mic tail-wait, device stop, encode, inference, network, paste — so
// it is the number to watch for "why did that feel slow", and it is emitted for
// batch and streaming providers alike, unlike the per-mode metrics lines.
// unaccounted_ms is the window minus every measured serial stage; a large value
// means something unmeasured (scheduling, updatesDone) is eating time.
func ReleaseToText(ms float64, b LatencyBreakdown) {
	if !logReady.Load() {
		return
	}
	serial := b.TailWaitMs + b.MicStopMs + b.ConvertMs + b.InferenceMs +
		b.ClipWaitMs + b.PasteCopyMs + b.PasteKeyMs
	ev := diagLog.Info().Float64("release_to_text_ms", ms)
	for _, f := range []struct {
		key string
		val float64
	}{
		{"tail_wait_ms", b.TailWaitMs},
		{"mic_stop_ms", b.MicStopMs},
		{"convert_ms", b.ConvertMs},
		{"inference_ms", b.InferenceMs},
		{"clip_save_ms", b.ClipSaveMs},
		{"clip_wait_ms", b.ClipWaitMs},
		{"paste_copy_ms", b.PasteCopyMs},
		{"paste_key_ms", b.PasteKeyMs},
	} {
		if f.val > 0 {
			ev = ev.Float64(f.key, f.val)
		}
	}
	ev.Float64("unaccounted_ms", ms-serial).Msg("felt_latency")
}

func TranscriptionText(text string) {
	if !logReady.Load() || transcribeFile == nil {
		return
	}
	logMu.Lock()
	defer logMu.Unlock()
	line := fmt.Sprintf("%s\t[%d]\t%s\n", time.Now().Format("2006-01-02 15:04:05"), pid, text)
	transcribeFile.WriteString(line)
}

func Confidence(confidence float64) {
	if !logReady.Load() {
		return
	}
	if confidence > 0 {
		diagLog.Info().Float64("confidence", confidence).Msg("api_confidence")
	}
}

type StreamMetricsData struct {
	Provider     string
	ConnectMs    float64
	FinalizeMs   float64
	TotalMs      float64
	AudioS       float64
	SentChunks   int
	SentKB       float64
	RecvMessages int
	RecvFinal    int
	CommitEvents int
	ProcessRSSMB float64
}

func StreamMetrics(m StreamMetricsData) {
	if !logReady.Load() {
		return
	}
	diagLog.Info().
		Str("provider", m.Provider).
		Float64("connect_ms", m.ConnectMs).
		Float64("finalize_ms", m.FinalizeMs).
		Float64("total_ms", m.TotalMs).
		Float64("audio_s", m.AudioS).
		Int("sent_chunks", m.SentChunks).
		Float64("sent_kb", m.SentKB).
		Int("recv_messages", m.RecvMessages).
		Int("recv_final", m.RecvFinal).
		Int("commit_events", m.CommitEvents).
		Float64("rss_mb", m.ProcessRSSMB).
		Msg("stream_transcription")
}

func SessionStart(provider, mode, format string) {
	if !logReady.Load() {
		return
	}
	diagLog.Info().
		Str("provider", provider).
		Str("mode", mode).
		Str("format", format).
		Msg("session_start")
}

func SessionEnd(count int) {
	if !logReady.Load() {
		return
	}
	diagLog.Info().
		Int("count", count).
		Msg("session_end")
}
