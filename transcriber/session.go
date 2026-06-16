package transcriber

import (
	"os"

	"github.com/shirou/gopsutil/v4/process"
)

// captureRSS records the process resident set size (from gopsutil, not Go's
// heap) — it includes cgo/mmap memory like the loaded model, so it's the one
// memory figure that's meaningful across every provider, local or remote.
func (r *SessionResult) captureRSS() {
	if p, err := process.NewProcess(int32(os.Getpid())); err == nil {
		if mi, err := p.MemoryInfo(); err == nil {
			r.ProcessRSSMB = float64(mi.RSS) / 1024 / 1024
		}
	}
}

type SessionConfig struct {
	Stream   bool
	Format   string // "mp3@16"|"mp3@64"|"flac" (batch only; ignored for streaming)
	Language string
	Hints    string // optional vocabulary hints for the model
}

type BatchStats struct {
	AudioLengthS     float64
	RawSizeKB        float64
	CompressedSizeKB float64
	CompressionPct   float64
	EncodeTimeMs     float64
	DNSTimeMs        float64
	TLSTimeMs        float64
	TTFBMs           float64
	TotalTimeMs      float64
	ConnReused       bool
	TLSProtocol      string
	Confidence       float64
	InferenceMs      float64
}

type StreamStats struct {
	ConnectMs    float64
	SentChunks   int
	SentKB       float64
	RecvMessages int
	RecvFinal    int
	RecvInterim  int
	CommitEvents int
	FinalizeMs   float64
	TotalMs      float64
	AudioS       float64
}

type SessionResult struct {
	Text          string
	HasText       bool
	NoSpeech      bool
	RateLimit     string // "remaining/limit" or empty
	ProcessRSSMB  float64      // resident set size (incl. cgo/mmap), all providers
	Batch         *BatchStats  // non-nil for batch sessions
	Stream        *StreamStats // non-nil for stream sessions
	Metrics       []string     // pre-formatted metric lines
	AudioData     []byte       // exact bytes sent to the model
	AudioFormat   string       // "mp3", "flac", or "wav"
}

type Session interface {
	// Feed delivers raw PCM audio. Implementations must copy pcm before returning;
	// the caller's buffer may be reused after Feed returns.
	Feed(pcm []byte)
	Updates() <-chan string
	Close() (SessionResult, error)
}
