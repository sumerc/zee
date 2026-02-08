package transcriber

import (
	"encoding/binary"
	"net/http"
	"testing"
	"time"
	"zee/encoder"
)

func TestNetworkMetricsSum(t *testing.T) {
	m := &NetworkMetrics{
		ConnWait:   10 * time.Millisecond,
		DNS:        20 * time.Millisecond,
		TCP:        30 * time.Millisecond,
		TLS:        40 * time.Millisecond,
		ReqHeaders: 5 * time.Millisecond,
		ReqBody:    15 * time.Millisecond,
		TTFB:       50 * time.Millisecond,
		Download:   25 * time.Millisecond,
	}
	got := m.Sum()
	want := 195 * time.Millisecond
	if got != want {
		t.Errorf("Sum() = %v, want %v", got, want)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	h := http.Header{}
	h.Set("X-Rate-Limit", "100")

	if got := firstNonEmpty(h, "X-Missing", "X-Rate-Limit"); got != "100" {
		t.Errorf("got %q, want %q", got, "100")
	}
	if got := firstNonEmpty(h, "X-A", "X-B"); got != "?" {
		t.Errorf("got %q, want %q", got, "?")
	}
}

func TestApiFormatFromConfig(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"flac", "flac"},
		{"mp3@16", "mp3"},
		{"mp3@64", "mp3"},
	} {
		t.Run(tt.input, func(t *testing.T) {
			if got := apiFormatFromConfig(tt.input); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewEncoder(t *testing.T) {
	for _, format := range []string{"mp3@16", "mp3@64", "flac"} {
		t.Run(format, func(t *testing.T) {
			enc, err := newEncoder(format)
			if err != nil {
				t.Fatalf("newEncoder(%q): %v", format, err)
			}
			if enc == nil {
				t.Fatalf("newEncoder(%q) returned nil", format)
			}
		})
	}
	t.Run("unknown", func(t *testing.T) {
		if _, err := newEncoder("ogg"); err == nil {
			t.Error("expected error for unknown format")
		}
	})
}

func TestBatchSessionFeedAndClose(t *testing.T) {
	fakeFn := func(audio []byte, format string) (*Result, error) {
		return &Result{
			Text:    "hello world",
			Metrics: &NetworkMetrics{TTFB: 10 * time.Millisecond},
		}, nil
	}

	cfg := SessionConfig{Format: "mp3@16"}
	bs, err := newBatchSession(cfg, fakeFn)
	if err != nil {
		t.Fatalf("newBatchSession: %v", err)
	}

	// Drain updates — channel closed by Close()
	go func() { for range bs.Updates() {} }()

	nSamples := encoder.BlockSize + encoder.BlockSize/2
	pcm := make([]byte, nSamples*2)
	for i := range nSamples {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(i%1000))
	}

	bs.Feed(pcm)

	result, err := bs.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if result.Text != "hello world" {
		t.Errorf("Text = %q, want %q", result.Text, "hello world")
	}
	if !result.HasText {
		t.Error("HasText should be true")
	}
	if result.Batch == nil {
		t.Fatal("Batch should be non-nil")
	}
	if result.Batch.AudioLengthS <= 0 {
		t.Error("AudioLengthS should be positive")
	}
}
