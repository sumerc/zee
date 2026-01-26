package encoder

import (
	"bytes"
	"sync"
	"time"

	"ses9000/internal/mp3"
)

const mp3FrameSamples = 576 // GRANULE_SIZE for MPEG II (16kHz)

type Mp3Encoder struct {
	buf         bytes.Buffer
	pending     []int16 // samples waiting for next frame
	enc         *mp3.Encoder
	totalFrames uint64
	encodeTime  time.Duration
	mu          sync.Mutex
}

func NewMp3(bitrate int) (*Mp3Encoder, error) {
	return &Mp3Encoder{
		enc: mp3.NewEncoder(SampleRate, Channels, bitrate),
	}, nil
}

func (e *Mp3Encoder) EncodeBlock(block []int16) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	start := time.Now()
	e.totalFrames += uint64(len(block))
	e.pending = append(e.pending, block...)

	// Encode complete frames only
	completeFrames := (len(e.pending) / mp3FrameSamples) * mp3FrameSamples
	if completeFrames > 0 {
		e.enc.Write(&e.buf, e.pending[:completeFrames])
		e.pending = e.pending[completeFrames:]
	}

	e.encodeTime += time.Since(start)
	return nil
}

func (e *Mp3Encoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(e.pending) > 0 {
		start := time.Now()
		// Pad to full frame
		for len(e.pending) < mp3FrameSamples {
			e.pending = append(e.pending, 0)
		}
		e.enc.Write(&e.buf, e.pending)
		e.encodeTime += time.Since(start)
	}
	return nil
}

func (e *Mp3Encoder) Bytes() []byte {
	return e.buf.Bytes()
}

func (e *Mp3Encoder) TotalFrames() uint64 {
	return e.totalFrames
}

func (e *Mp3Encoder) AddEncodeTime(d time.Duration) {
	e.mu.Lock()
	e.encodeTime += d
	e.mu.Unlock()
}

func (e *Mp3Encoder) EncodeTime() time.Duration {
	return e.encodeTime
}
