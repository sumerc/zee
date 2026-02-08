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
	errorSamples []byte
	soundOnce    sync.Once

	// Playback state - accessed atomically from callback
	playSamples atomic.Pointer[[]byte]
	playPos     atomic.Uint32
	playMu      sync.Mutex
)

func initDevice() error {
	config := malgo.DefaultDeviceConfig(malgo.Playback)
	config.Playback.Format = malgo.FormatS16
	config.Playback.Channels = 1
	config.SampleRate = sampleRate

	callbacks := malgo.DeviceCallbacks{
		Data: dataCallback,
	}

	var err error
	device, err = malgo.InitDevice(malgoCtx.Context, config, callbacks)
	return err
}

func initSound() {
	var err error
	malgoCtx, err = malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return
	}

	startSamples = generateTickBytes(sampleRate, startFreq, 0.03, startVolume, startDecay)
	endSamples = generateTickBytes(sampleRate, endFreq, 0.05, endVolume, endDecay)
	errorSamples = generateDoubleBeepBytes(sampleRate, errorFreq, 0.08, 0.05, errorVolume, errorDecay)

	if err := initDevice(); err != nil {
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
		playSamples.Store(nil)
		for i := range pOutput {
			pOutput[i] = 0
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

func generateDoubleBeepBytes(sampleRate int, freq float64, beepDur float64, gapDur float64, volume float64, decay float64) []byte {
	beep := generateTickBytes(sampleRate, freq, beepDur, volume, decay)
	gap := make([]byte, int(float64(sampleRate)*gapDur)*2)
	result := make([]byte, 0, len(beep)*2+len(gap))
	result = append(result, beep...)
	result = append(result, gap...)
	result = append(result, beep...)
	return result
}

func playBytes(samples []byte) {
	if malgoCtx == nil || len(samples) == 0 {
		return
	}

	playMu.Lock()
	defer playMu.Unlock()

	if device == nil {
		return
	}

	// Stop device first to ensure clean state (no-op if not running)
	device.Stop()

	// Set up playback state
	playPos.Store(0)
	playSamples.Store(&samples)

	// Start device
	if err := device.Start(); err != nil {
		// Try recreating device (handles macOS sleep/wake)
		device.Uninit()
		if err := initDevice(); err != nil {
			playSamples.Store(nil)
			return
		}
		if err := device.Start(); err != nil {
			playSamples.Store(nil)
			return
		}
	}
}

func Init() {
	soundOnce.Do(initSound)
}

func PlayStart() {
	if disabled {
		return
	}
	soundOnce.Do(initSound)
	playBytes(startSamples)
}

func PlayEnd() {
	if disabled {
		return
	}
	soundOnce.Do(initSound)
	playBytes(endSamples)
}

func PlayError() {
	if disabled {
		return
	}
	soundOnce.Do(initSound)
	playBytes(errorSamples)
}
