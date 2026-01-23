package encoder

import (
	"bytes"
	"sync"
	"time"

	"ses9000/internal/mp3"
)

type Mp3Encoder struct {
	buf         bytes.Buffer
	samples     []int16
	totalFrames uint64
	encodeTime  time.Duration
	mu          sync.Mutex
	bitrate     int
}

func NewMp3(bitrate int) (*Mp3Encoder, error) {
	return &Mp3Encoder{bitrate: bitrate}, nil
}

func (e *Mp3Encoder) EncodeBlock(block []int16) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.samples = append(e.samples, block...)
	e.totalFrames += uint64(len(block))
	return nil
}

func (e *Mp3Encoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(e.samples) == 0 {
		return nil
	}

	start := time.Now()
	enc := mp3.NewEncoder(SampleRate, Channels, e.bitrate)
	enc.Write(&e.buf, e.samples)
	e.encodeTime += time.Since(start)

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
