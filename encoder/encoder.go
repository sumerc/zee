package encoder

import "time"

const (
	SampleRate    = 16000
	Channels      = 1
	BitsPerSample = 16
	BlockSize     = 4096
)

type Encoder interface {
	EncodeBlock(block []int16) error
	Close() error
	Bytes() []byte
	TotalFrames() uint64
	AddEncodeTime(d time.Duration)
	EncodeTime() time.Duration
}
