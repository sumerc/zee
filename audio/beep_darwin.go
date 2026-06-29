//go:build darwin

package audio

import (
	"sync"
	"sync/atomic"

	"github.com/gen2brain/malgo"
)

var (
	playbackDevice *malgo.Device

	// Playback state, read from the device callback: the canonical mono buffer
	// being played and the current sample offset into it.
	playMono atomic.Pointer[[]int16]
	playPos  atomic.Uint32
	playMu   sync.Mutex
)

// initPlaybackDevice is lock-free; callers must hold deviceMu around it.
func initPlaybackDevice() error {
	config := malgo.DefaultDeviceConfig(malgo.Playback)
	config.Playback.Format = malgo.FormatS16
	config.Playback.Channels = 1
	config.SampleRate = beepSampleRate

	callbacks := malgo.DeviceCallbacks{
		Data: dataCallback,
	}

	var err error
	playbackDevice, err = malgo.InitDevice(maCtx.Context, config, callbacks)
	return err
}

func initSound() {
	deviceMu.Lock()
	defer deviceMu.Unlock()

	if err := ensureContext(); err != nil {
		return
	}

	buildSamples()
	initPlaybackDevice() // best-effort; playInt16 reinits per play anyway
}

// dataCallback fills the mono S16 device buffer, converting each canonical
// int16 sample to two little-endian bytes as it copies.
func dataCallback(pOutput, _ []byte, frameCount uint32) {
	silence := func() {
		for i := range pOutput {
			pOutput[i] = 0
		}
	}

	mono := playMono.Load()
	if mono == nil {
		silence()
		return
	}
	pos := playPos.Load()
	total := uint32(len(*mono))
	if pos >= total {
		playMono.Store(nil)
		silence()
		return
	}

	frames := min(total-pos, frameCount)
	s := *mono
	for i := uint32(0); i < frames; i++ {
		v := s[pos+i]
		pOutput[i*2] = byte(v)
		pOutput[i*2+1] = byte(v >> 8)
	}
	for i := frames * 2; i < frameCount*2; i++ {
		pOutput[i] = 0 // zero-fill any frames past the buffer
	}
	playPos.Store(pos + frames)
}

func playInt16(mono []int16) {
	if maCtx == nil || len(mono) == 0 {
		return
	}

	playMu.Lock()
	defer playMu.Unlock()

	deviceMu.Lock()
	defer deviceMu.Unlock()

	// Always reinitialize to pick up current default output device
	// (handles BT connect/disconnect, sleep/wake). Null device after Uninit
	// so a failed reinit can't leave it pointing at the freed device — the
	// next call would otherwise Uninit it again and double-free.
	if playbackDevice != nil {
		playbackDevice.Stop()
		playbackDevice.Uninit()
		playbackDevice = nil
	}
	if err := initPlaybackDevice(); err != nil {
		playbackDevice = nil
		return
	}

	playPos.Store(0)
	playMono.Store(&mono)

	if err := playbackDevice.Start(); err != nil {
		playMono.Store(nil)
	}
}

func playOne(s sound) {
	playInt16(samples[s])
}
