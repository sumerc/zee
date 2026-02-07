package transcriber

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

type NetworkMetrics struct {
	DNS        time.Duration
	ConnWait   time.Duration
	TCP        time.Duration
	TLS        time.Duration
	ReqHeaders time.Duration
	ReqBody    time.Duration
	TTFB       time.Duration
	Download   time.Duration
	Total      time.Duration
	ConnReused  bool
	TLSProtocol string
}

func (m *NetworkMetrics) Sum() time.Duration {
	return m.ConnWait + m.DNS + m.TCP + m.TLS + m.ReqHeaders + m.ReqBody + m.TTFB + m.Download
}

func firstNonEmpty(h http.Header, keys ...string) string {
	for _, k := range keys {
		if v := h.Get(k); v != "" {
			return v
		}
	}
	return "?"
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
	Confidence   float64
	NoSpeechProb float64
	AvgLogProb   float64
	Duration     float64
	Segments     []Segment
}

type Transcriber interface {
	Name() string
	SetLanguage(lang string)
	GetLanguage() string
	NewSession(ctx context.Context, cfg SessionConfig) (Session, error)
}

type baseTranscriber struct {
	client *TracedClient
	apiURL string
	lang   string
}

func (b *baseTranscriber) SetLanguage(lang string) { b.lang = lang }

func (b *baseTranscriber) GetLanguage() string { return b.lang }

func New() (Transcriber, error) {
	dgKey := os.Getenv("DEEPGRAM_API_KEY")
	groqKey := os.Getenv("GROQ_API_KEY")

	if dgKey != "" {
		return NewDeepgram(dgKey), nil
	}
	if groqKey != "" {
		return NewGroq(groqKey), nil
	}

	return nil, fmt.Errorf("set DEEPGRAM_API_KEY or GROQ_API_KEY environment variable")
}
