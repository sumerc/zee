package transcriber

import (
	"context"
	"fmt"
	"time"
)

type FakeTranscriber struct {
	text string
	err  error
	lang string
}

func NewFake(text string, err error) *FakeTranscriber {
	return &FakeTranscriber{text: text, err: err}
}

func (f *FakeTranscriber) Name() string              { return "fake" }
func (f *FakeTranscriber) SetLanguage(lang string)    { f.lang = lang }
func (f *FakeTranscriber) GetLanguage() string        { return f.lang }

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
	return &fakeSession{text: f.text, err: f.err, updates: updates}, nil
}

type fakeSession struct {
	text    string
	err     error
	updates chan string
}

func (s *fakeSession) Feed([]byte) {}

func (s *fakeSession) Updates() <-chan string { return s.updates }

func (s *fakeSession) Close() (SessionResult, error) {
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
	r.captureMemStats()
	return r, nil
}
