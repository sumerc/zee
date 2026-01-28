//go:build darwin

package beep

import (
	"math"
	"sync"
	"time"

	"github.com/gen2brain/malgo"
)

var (
	malgoCtx     *malgo.AllocatedContext
	startSamples []byte
	endSamples   []byte
	soundOnce    sync.Once
	soundMu      sync.Mutex
)

func initSound() {
	var err error
	malgoCtx, err = malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return
	}

	startSamples = generateTickBytes(44100, 1200, 0.03, 0.5, 60)
	endSamples = generateTickBytes(44100, 900, 0.05, 0.5, 40)
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
	if malgoCtx == nil || len(samples) == 0 {
		return
	}

	soundMu.Lock()
	defer soundMu.Unlock()

	config := malgo.DefaultDeviceConfig(malgo.Playback)
	config.Playback.Format = malgo.FormatS16
	config.Playback.Channels = 1
	config.SampleRate = 44100

	var pos uint32
	total := uint32(len(samples))

	callbacks := malgo.DeviceCallbacks{
		Data: func(pOutput, _ []byte, frameCount uint32) {
			bytesToWrite := frameCount * 2
			remaining := total - pos
			if bytesToWrite > remaining {
				bytesToWrite = remaining
			}
			if bytesToWrite > 0 {
				copy(pOutput[:bytesToWrite], samples[pos:pos+bytesToWrite])
				pos += bytesToWrite
			}
			for i := bytesToWrite; i < frameCount*2; i++ {
				pOutput[i] = 0
			}
		},
	}

	device, err := malgo.InitDevice(malgoCtx.Context, config, callbacks)
	if err != nil {
		return
	}

	if err := device.Start(); err != nil {
		device.Uninit()
		return
	}

	for pos < total {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)

	device.Stop()
	device.Uninit()
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
