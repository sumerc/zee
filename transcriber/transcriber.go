package transcriber

import (
	"context"
	"fmt"
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

func (b *baseTranscriber) warmConnection() time.Duration {
	return b.client.WarmConnection(b.apiURL)
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
