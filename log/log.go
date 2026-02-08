package log

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

var (
	diagLog        zerolog.Logger
	diagFile       *os.File
	transcribeFile *os.File
	logMu          sync.Mutex
	logReady       bool
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
	MemoryAllocMB    float64
	MemoryPeakMB     float64
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

func Init() error {
	logMu.Lock()
	defer logMu.Unlock()

	if err := EnsureDir(); err != nil {
		return err
	}

	pid = os.Getpid()

	var err error

	diagPath := filepath.Join(dir, "diagnostics_log.txt")
	diagFile, err = os.OpenFile(diagPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	transcribePath := filepath.Join(dir, "transcribe_log.txt")
	transcribeFile, err = os.OpenFile(transcribePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		diagFile.Close()
		return err
	}

	consoleWriter := zerolog.ConsoleWriter{
		Out:        diagFile,
		TimeFormat: "2006-01-02 15:04:05",
		NoColor:    true,
	}
	diagLog = zerolog.New(consoleWriter).With().Timestamp().Int("pid", pid).Logger()

	logReady = true
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
	logReady = false
}

func Info(msg string) {
	if logReady {
		diagLog.Info().Msg(msg)
	}
}

func Error(msg string) {
	if logReady {
		diagLog.Error().Msg(msg)
	}
}

func Errorf(format string, args ...any) {
	if logReady {
		diagLog.Error().Msg(fmt.Sprintf(format, args...))
	}
}

func Warn(msg string) {
	if logReady {
		diagLog.Warn().Msg(msg)
	}
}

func Warnf(format string, args ...any) {
	if logReady {
		diagLog.Warn().Msg(fmt.Sprintf(format, args...))
	}
}

func TranscriptionMetrics(m Metrics, mode, format, provider string, connReused bool, tlsProto string) {
	if !logReady {
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
		Float64("mem_mb", m.MemoryAllocMB).
		Float64("peak_mb", m.MemoryPeakMB).
		Msg("transcription")
}

func TranscriptionText(text string) {
	if !logReady {
		return
	}
	logMu.Lock()
	defer logMu.Unlock()
	line := fmt.Sprintf("%s\t[%d]\t%s\n", time.Now().Format("2006-01-02 15:04:05"), pid, text)
	transcribeFile.WriteString(line)
}

func Confidence(confidence float64) {
	if !logReady {
		return
	}
	if confidence > 0 {
		diagLog.Info().Float64("confidence", confidence).Msg("api_confidence")
	}
}

type StreamMetricsData struct {
	ConnectMs    float64
	FinalizeMs   float64
	TotalMs      float64
	AudioS       float64
	SentChunks   int
	SentKB       float64
	RecvMessages int
	RecvFinal    int
	CommitEvents int
}

func StreamMetrics(m StreamMetricsData) {
	if !logReady {
		return
	}
	diagLog.Info().
		Float64("connect_ms", m.ConnectMs).
		Float64("finalize_ms", m.FinalizeMs).
		Float64("total_ms", m.TotalMs).
		Float64("audio_s", m.AudioS).
		Int("sent_chunks", m.SentChunks).
		Float64("sent_kb", m.SentKB).
		Int("recv_messages", m.RecvMessages).
		Int("recv_final", m.RecvFinal).
		Int("commit_events", m.CommitEvents).
		Msg("stream_transcription")
}

func SessionStart(provider, mode, format string) {
	if !logReady {
		return
	}
	diagLog.Info().
		Str("provider", provider).
		Str("mode", mode).
		Str("format", format).
		Msg("session_start")
}

func SessionEnd(count int) {
	if !logReady {
		return
	}
	diagLog.Info().
		Int("count", count).
		Msg("session_end")
}
