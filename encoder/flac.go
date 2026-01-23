package encoder

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
)

type FlacEncoder struct {
	buf         bytes.Buffer
	enc         *flac.Encoder
	totalFrames uint64
	encodeTime  time.Duration
	mu          sync.Mutex
}

func NewFlac() (*FlacEncoder, error) {
	e := &FlacEncoder{}
	info := &meta.StreamInfo{
		BlockSizeMin:  BlockSize,
		BlockSizeMax:  BlockSize,
		SampleRate:    SampleRate,
		NChannels:     Channels,
		BitsPerSample: BitsPerSample,
		NSamples:      0,
	}
	enc, err := flac.NewEncoder(&e.buf, info)
	if err != nil {
		return nil, fmt.Errorf("creating flac encoder: %w", err)
	}
	enc.EnablePredictionAnalysis(true)
	e.enc = enc
	return e, nil
}

func (e *FlacEncoder) EncodeBlock(block []int16) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	samples32 := make([]int32, len(block))
	for i, s := range block {
		samples32[i] = int32(s)
	}

	subframe := &frame.Subframe{
		SubHeader: frame.SubHeader{
			Pred: frame.PredVerbatim,
		},
		Samples:  samples32,
		NSamples: len(block),
	}

	f := &frame.Frame{
		Header: frame.Header{
			BlockSize:     uint16(len(block)),
			SampleRate:    SampleRate,
			Channels:      frame.ChannelsMono,
			BitsPerSample: BitsPerSample,
		},
		Subframes: []*frame.Subframe{subframe},
	}

	if err := e.enc.WriteFrame(f); err != nil {
		return fmt.Errorf("writing flac frame: %w", err)
	}
	e.totalFrames += uint64(len(block))
	return nil
}

func (e *FlacEncoder) Close() error {
	return e.enc.Close()
}

func (e *FlacEncoder) Bytes() []byte {
	return e.buf.Bytes()
}

func (e *FlacEncoder) TotalFrames() uint64 {
	return e.totalFrames
}

func (e *FlacEncoder) AddEncodeTime(d time.Duration) {
	e.mu.Lock()
	e.encodeTime += d
	e.mu.Unlock()
}

func (e *FlacEncoder) EncodeTime() time.Duration {
	return e.encodeTime
}
