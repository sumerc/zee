package encoder

import (
	"sync"
	"time"
)

type AdaptiveEncoder struct {
	mp3_16      *Mp3Encoder
	mp3_64      *Mp3Encoder
	flac        *FlacEncoder
	chosen      string // "mp3_16", "mp3_64", or "flac"
	totalFrames uint64
	mu          sync.Mutex
}

func NewAdaptive() (*AdaptiveEncoder, error) {
	mp3_16, err := NewMp3(16)
	if err != nil {
		return nil, err
	}
	mp3_64, err := NewMp3(64)
	if err != nil {
		return nil, err
	}
	flac, err := NewFlac()
	if err != nil {
		return nil, err
	}
	return &AdaptiveEncoder{
		mp3_16: mp3_16,
		mp3_64: mp3_64,
		flac:   flac,
		chosen: "flac", // default to highest quality
	}, nil
}

func (e *AdaptiveEncoder) EncodeBlock(block []int16) error {
	e.mu.Lock()
	e.totalFrames += uint64(len(block))
	e.mu.Unlock()

	// Fan out to all encoders concurrently
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); e.mp3_16.EncodeBlock(block) }()
	go func() { defer wg.Done(); e.mp3_64.EncodeBlock(block) }()
	go func() { defer wg.Done(); e.flac.EncodeBlock(block) }()
	wg.Wait()
	return nil
}

func (e *AdaptiveEncoder) Close() error {
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); e.mp3_16.Close() }()
	go func() { defer wg.Done(); e.mp3_64.Close() }()
	go func() { defer wg.Done(); e.flac.Close() }()
	wg.Wait()
	return nil
}

// Select picks the highest quality format under the threshold
func (e *AdaptiveEncoder) Select(threshold int) {
	flacSize := len(e.flac.Bytes())
	mp3_64Size := len(e.mp3_64.Bytes())

	if flacSize <= threshold {
		e.chosen = "flac"
	} else if mp3_64Size <= threshold {
		e.chosen = "mp3_64"
	} else {
		e.chosen = "mp3_16"
	}
}

func (e *AdaptiveEncoder) Bytes() []byte {
	switch e.chosen {
	case "flac":
		return e.flac.Bytes()
	case "mp3_64":
		return e.mp3_64.Bytes()
	default:
		return e.mp3_16.Bytes()
	}
}

// Format returns "mp3" or "flac" for API compatibility
func (e *AdaptiveEncoder) Format() string {
	if e.chosen == "flac" {
		return "flac"
	}
	return "mp3"
}

// Bitrate returns the bitrate of the chosen format (0 for FLAC)
func (e *AdaptiveEncoder) Bitrate() int {
	switch e.chosen {
	case "mp3_16":
		return 16
	case "mp3_64":
		return 64
	default:
		return 0
	}
}

// ChosenName returns human-readable name like "flac", "mp3@64", "mp3@16"
func (e *AdaptiveEncoder) ChosenName() string {
	switch e.chosen {
	case "flac":
		return "flac"
	case "mp3_64":
		return "mp3@64"
	default:
		return "mp3@16"
	}
}

// AllSizes returns sizes of all three encoded formats for display
func (e *AdaptiveEncoder) AllSizes() (flac, mp3_64, mp3_16 int) {
	return len(e.flac.Bytes()), len(e.mp3_64.Bytes()), len(e.mp3_16.Bytes())
}

func (e *AdaptiveEncoder) TotalFrames() uint64 {
	return e.totalFrames
}

func (e *AdaptiveEncoder) AddEncodeTime(d time.Duration) {
	// Sub-encoders track their own time; this is a no-op for adaptive
}

func (e *AdaptiveEncoder) EncodeTime() time.Duration {
	t1 := e.mp3_16.EncodeTime()
	if t2 := e.mp3_64.EncodeTime(); t2 > t1 {
		t1 = t2
	}
	if t3 := e.flac.EncodeTime(); t3 > t1 {
		t1 = t3
	}
	return t1
}
