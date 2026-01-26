package transcriber

import (
	"fmt"
	"os"
	"time"
)

type NetworkMetrics struct {
	DNS        time.Duration
	ConnWait   time.Duration // time waiting for connection from pool
	TCP        time.Duration // TCP connect time
	TLS        time.Duration
	ReqHeaders time.Duration // time to write request headers
	ReqBody    time.Duration // time to write request body
	TTFB       time.Duration // time to first byte (after request sent)
	Download   time.Duration // time to read response body
	Total      time.Duration
	ConnReused bool
}

type Segment struct {
	Text             string
	NoSpeechProb     float64
	AvgLogProb       float64
	CompressionRatio float64
	Temperature      float64
	Start            float64
	End              float64
}

type Result struct {
	Text         string
	Metrics      *NetworkMetrics
	RateLimit    string
	Confidence   float64   // 0.0-1.0, overall transcription confidence
	NoSpeechProb float64   // 0.0-1.0, probability of no speech (Whisper only)
	AvgLogProb   float64   // average log probability (Whisper only)
	Duration     float64   // audio duration as reported by API (seconds)
	Segments     []Segment // per-segment details (Whisper only)
}

type Transcriber interface {
	Transcribe(audio []byte, format string) (*Result, error)
	WarmConnection() time.Duration // returns TLS handshake time
	Name() string
}

func New() Transcriber {
	dgKey := os.Getenv("DEEPGRAM_API_KEY")
	groqKey := os.Getenv("GROQ_API_KEY")

	if dgKey != "" {
		return NewDeepgram(dgKey)
	}
	if groqKey != "" {
		return NewGroq(groqKey)
	}

	fmt.Println("Error: set DEEPGRAM_API_KEY or GROQ_API_KEY environment variable")
	os.Exit(1)
	return nil
}
