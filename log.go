package main

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"ses9000/transcriber"
)

var (
	diagLog        zerolog.Logger
	diagFile       *os.File
	transcribeFile *os.File
	logMu          sync.Mutex
	logReady       bool
)

func initLogging() error {
	logMu.Lock()
	defer logMu.Unlock()

	var err error

	diagFile, err = os.OpenFile("diagnostics_log.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	transcribeFile, err = os.OpenFile("transcribe_log.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		diagFile.Close()
		return err
	}

	consoleWriter := zerolog.ConsoleWriter{
		Out:        diagFile,
		TimeFormat: "15:04:05",
		NoColor:    true,
	}
	diagLog = zerolog.New(consoleWriter).With().Timestamp().Logger()

	logReady = true
	return nil
}

func closeLogging() {
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

func logDiagInfo(msg string) {
	if logReady {
		diagLog.Info().Msg(msg)
	}
}

func logDiagError(msg string) {
	if logReady {
		diagLog.Error().Msg(msg)
	}
}

func logDiagWarn(msg string) {
	if logReady {
		diagLog.Warn().Msg(msg)
	}
}

func logTranscriptionMetrics(r TranscriptionRecord, mode, format, provider string, connReused bool) {
	if !logReady {
		return
	}

	connStatus := "new"
	if connReused {
		connStatus = "reused"
	}

	diagLog.Info().
		Str("mode", mode).
		Str("format", format).
		Str("provider", provider).
		Str("conn", connStatus).
		Float64("audio_s", r.AudioLengthS).
		Float64("raw_kb", r.RawSizeKB).
		Float64("compressed_kb", r.CompressedSizeKB).
		Float64("compression_pct", r.CompressionPct).
		Float64("encode_ms", r.EncodeTimeMs).
		Float64("dns_ms", r.DNSTimeMs).
		Float64("tls_ms", r.TLSTimeMs).
		Float64("net_infer_ms", r.NetInferMs).
		Float64("total_ms", r.TotalTimeMs).
		Float64("mem_mb", r.MemoryAllocMB).
		Float64("peak_mb", r.MemoryPeakMB).
		Msg("transcription")
}

func logTranscriptionText(text string) {
	if !logReady {
		return
	}
	logMu.Lock()
	defer logMu.Unlock()
	line := fmt.Sprintf("%s\t%s\n", time.Now().Format("2006-01-02 15:04:05"), text)
	transcribeFile.WriteString(line)
}

func logSegments(segments []transcriber.Segment, confidence float64) {
	if !logReady {
		return
	}
	if confidence > 0 {
		diagLog.Info().Float64("confidence", confidence).Msg("api_confidence")
	}
	for i, seg := range segments {
		diagLog.Info().
			Int("seg", i).
			Float64("no_speech", seg.NoSpeechProb).
			Float64("logprob", seg.AvgLogProb).
			Float64("comp_ratio", seg.CompressionRatio).
			Float64("temp", seg.Temperature).
			Float64("start", seg.Start).
			Float64("end", seg.End).
			Msg("segment")
	}
}

func logSessionStart(provider, mode, format string) {
	if !logReady {
		return
	}
	diagLog.Info().
		Str("provider", provider).
		Str("mode", mode).
		Str("format", format).
		Msg("session_start")
}

func logSessionEnd(count int) {
	if !logReady {
		return
	}
	diagLog.Info().
		Int("count", count).
		Msg("session_end")
}
