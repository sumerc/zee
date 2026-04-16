package transcriber

import "runtime"

func (r *SessionResult) captureMemStats() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	r.MemoryAllocMB = float64(m.Alloc) / 1024 / 1024
	r.MemoryPeakMB = float64(m.TotalAlloc) / 1024 / 1024
}

type SessionConfig struct {
	Stream   bool
	Format   string // "mp3@16"|"mp3@64"|"flac" (batch only; ignored for streaming)
	Language string
	Hint     string // optional vocabulary hints for the model
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
	RateLimit     string       // "remaining/limit" or empty
	MemoryAllocMB float64
	MemoryPeakMB  float64
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
