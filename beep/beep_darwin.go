//go:build darwin

package beep

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/gen2brain/malgo"
)

var (
	malgoCtx     *malgo.AllocatedContext
	device       *malgo.Device
	startSamples []byte
	endSamples   []byte
	soundOnce    sync.Once

	// Playback state - accessed atomically from callback
	playSamples atomic.Pointer[[]byte]
	playPos     atomic.Uint32
	playDone    chan struct{}
	playMu      sync.Mutex
)

func initSound() {
	var err error
	malgoCtx, err = malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return
	}

	startSamples = generateTickBytes(44100, 1200, 0.03, 0.5, 60)
	endSamples = generateTickBytes(44100, 900, 0.05, 0.5, 40)

	// Create persistent device
	config := malgo.DefaultDeviceConfig(malgo.Playback)
	config.Playback.Format = malgo.FormatS16
	config.Playback.Channels = 1
	config.SampleRate = 44100

	callbacks := malgo.DeviceCallbacks{
		Data: dataCallback,
	}

	device, err = malgo.InitDevice(malgoCtx.Context, config, callbacks)
	if err != nil {
		malgoCtx.Uninit()
		malgoCtx = nil
		return
	}
}

func dataCallback(pOutput, _ []byte, frameCount uint32) {
	samples := playSamples.Load()
	if samples == nil || len(*samples) == 0 {
		// Silence when not playing
		for i := range pOutput {
			pOutput[i] = 0
		}
		return
	}

	pos := playPos.Load()
	total := uint32(len(*samples))
	bytesToWrite := frameCount * 2
	remaining := total - pos

	if remaining == 0 {
		// Done playing - signal and output silence
		for i := range pOutput {
			pOutput[i] = 0
		}
		select {
		case playDone <- struct{}{}:
		default:
		}
		return
	}

	if bytesToWrite > remaining {
		bytesToWrite = remaining
	}

	copy(pOutput[:bytesToWrite], (*samples)[pos:pos+bytesToWrite])
	playPos.Store(pos + bytesToWrite)

	// Zero-fill remainder
	for i := bytesToWrite; i < frameCount*2; i++ {
		pOutput[i] = 0
	}
}

func generateTickBytes(sampleRate int, freq float64, duration float64, volume float64, decay float64) []byte {
	n := int(float64(sampleRate) * duration)
	buf := make([]byte, n*2)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(sampleRate)
		envelope := math.Exp(-t * decay)
		sample := int16(math.Sin(2*math.Pi*freq*t) * 32767 * volume * envelope)
		buf[i*2] = byte(sample)
		buf[i*2+1] = byte(sample >> 8)
	}
	return buf
}

func playBytes(samples []byte) {
	if device == nil || len(samples) == 0 {
		return
	}

	playMu.Lock()
	defer playMu.Unlock()

	// Set up playback state
	playDone = make(chan struct{}, 1)
	playPos.Store(0)
	playSamples.Store(&samples)

	// Start device if not running
	if !device.IsStarted() {
		if err := device.Start(); err != nil {
			playSamples.Store(nil)
			return
		}
	}

	// Wait for playback to complete
	<-playDone
	playSamples.Store(nil)
}

func Init() {
	soundOnce.Do(initSound)
}

func PlayStart() {
	soundOnce.Do(initSound)
	go playBytes(startSamples)
}

func PlayEnd() {
	soundOnce.Do(initSound)
	go playBytes(endSamples)
}
