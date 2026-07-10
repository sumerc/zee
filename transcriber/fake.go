package transcriber

import (
	"context"
	"fmt"
	"os"
	"time"
)

type FakeTranscriber struct {
	text   string
	err    error
	lang   string
	stream bool
	delay  time.Duration // simulated inference time (Close blocks for this long)
}

func NewFake(text string, err error) *FakeTranscriber {
	f := &FakeTranscriber{text: text, err: err, stream: os.Getenv("ZEE_FAKE_STREAM") == "1"}
	if d := os.Getenv("ZEE_FAKE_DELAY"); d != "" {
		f.delay, _ = time.ParseDuration(d)
	}
	return f
}

// SetDelay makes Close block for d, simulating inference latency (for tests that
// need a window where transcription is in progress).
func (f *FakeTranscriber) SetDelay(d time.Duration) { f.delay = d }

func (f *FakeTranscriber) Name() string                   { return "fake" }
func (f *FakeTranscriber) SupportedLanguages() []Language { return nil }
func (f *FakeTranscriber) SetLanguage(lang string)  { f.lang = lang }
func (f *FakeTranscriber) GetLanguage() string      { return f.lang }
func (f *FakeTranscriber) Models() []ModelInfo {
	if f.stream {
		return []ModelInfo{{ID: "fake-stream", Label: "fake (stream)", Stream: true}}
	}
	return nil
}
func (f *FakeTranscriber) SetModel(_ string)        {}
func (f *FakeTranscriber) GetModel() string {
	if f.stream {
		return "fake-stream"
	}
	return ""
}

func (f *FakeTranscriber) NewSession(_ context.Context, cfg SessionConfig) (Session, error) {
	updates := make(chan string, 1)
	if cfg.Stream && f.text != "" {
		go func() {
			time.Sleep(100 * time.Millisecond)
			updates <- f.text
			close(updates)
		}()
	} else {
		close(updates)
	}
	return &fakeSession{text: f.text, err: f.err, updates: updates, delay: f.delay}, nil
}

type fakeSession struct {
	text    string
	err     error
	updates chan string
	delay   time.Duration
}

func (s *fakeSession) Feed([]byte) {}

func (s *fakeSession) Updates() <-chan string { return s.updates }

func (s *fakeSession) Close() (SessionResult, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.err != nil {
		return SessionResult{}, fmt.Errorf("fake transcriber error: %w", s.err)
	}
	r := SessionResult{
		Text:    s.text,
		HasText: s.text != "",
		Batch: &BatchStats{
			AudioLengthS: 1.0,
			TotalTimeMs:  10,
		},
		Metrics: []string{"total: 10ms (fake)"},
	}
	r.captureRSS()
	return r, nil
}
