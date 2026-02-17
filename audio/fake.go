package audio

import (
	"os"
	"sync"
	"time"
	"zee/encoder"
)

const (
	fakeFrameSize     = 1024
	fakeBytesPerFrame = 2 // 16-bit mono
)

type FakeContext struct {
	pcm      []byte
	realtime bool
}

func NewFakeContext(wavPath string, realtime bool) (*FakeContext, error) {
	data, err := os.ReadFile(wavPath)
	if err != nil {
		return nil, err
	}
	if len(data) > WAVHeaderSize {
		data = data[WAVHeaderSize:]
	}
	return &FakeContext{pcm: data, realtime: realtime}, nil
}

func (f *FakeContext) Devices() ([]DeviceInfo, error) { return nil, nil }
func (f *FakeContext) Close()                         {}

func (f *FakeContext) NewCapture(_ *DeviceInfo, _ CaptureConfig) (CaptureDevice, error) {
	return &FakeCapture{pcm: f.pcm, realtime: f.realtime, audioDone: make(chan struct{})}, nil
}

type FakeCapture struct {
	pcm       []byte
	realtime  bool
	audioDone chan struct{}

	mu       sync.Mutex
	cb       DataCallback
	stopCh   chan struct{}
	feedDone chan struct{}
}

func (f *FakeCapture) AudioDone() <-chan struct{} { return f.audioDone }

func (f *FakeCapture) SetCallback(cb DataCallback) {
	f.mu.Lock()
	f.cb = cb
	f.mu.Unlock()
}

func (f *FakeCapture) ClearCallback() {
	f.mu.Lock()
	f.cb = nil
	f.mu.Unlock()
}

func (f *FakeCapture) DeviceName() string { return "fake" }

func (f *FakeCapture) feedChunk(cb DataCallback, pos, chunkBytes int) int {
	end := min(pos+chunkBytes, len(f.pcm))
	chunk := make([]byte, end-pos)
	copy(chunk, f.pcm[pos:end])
	cb(chunk, uint32(len(chunk)/fakeBytesPerFrame))
	return end
}

func (f *FakeCapture) Start() error {
	f.stopCh = make(chan struct{})
	f.feedDone = make(chan struct{})
	// audioDone is NOT recreated here -- callers may already be waiting on it.
	// It's reset in Stop() for replay.

	chunkBytes := fakeFrameSize * fakeBytesPerFrame

	if !f.realtime {
		f.mu.Lock()
		cb := f.cb
		f.mu.Unlock()
		if cb != nil {
			for pos := 0; pos < len(f.pcm); {
				pos = f.feedChunk(cb, pos, chunkBytes)
			}
		}
		close(f.audioDone)

		go func() {
			defer close(f.feedDone)
			silence := make([]byte, chunkBytes)
			for {
				select {
				case <-f.stopCh:
					return
				case <-time.After(time.Millisecond):
				}
				f.mu.Lock()
				cb := f.cb
				f.mu.Unlock()
				if cb != nil {
					cb(silence, fakeFrameSize)
				}
			}
		}()
	} else {
		interval := time.Duration(fakeFrameSize) * time.Second / time.Duration(encoder.SampleRate)
		go func() {
			defer close(f.feedDone)
			pos := 0
			silence := make([]byte, chunkBytes)
			audioFinished := false

			for {
				select {
				case <-f.stopCh:
					return
				default:
				}

				f.mu.Lock()
				cb := f.cb
				f.mu.Unlock()
				if cb == nil {
					time.Sleep(time.Millisecond)
					continue
				}

				if pos < len(f.pcm) {
					pos = f.feedChunk(cb, pos, chunkBytes)
				} else {
					if !audioFinished {
						audioFinished = true
						close(f.audioDone)
					}
					cb(silence, fakeFrameSize)
				}

				select {
				case <-f.stopCh:
					return
				case <-time.After(interval):
				}
			}
		}()
	}

	return nil
}

func (f *FakeCapture) Stop() {
	select {
	case <-f.stopCh:
	default:
		close(f.stopCh)
	}
	if f.feedDone != nil {
		<-f.feedDone
	}
	f.audioDone = make(chan struct{}) // reset for replay
}

func (f *FakeCapture) Close() {}
