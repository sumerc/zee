//go:build linux

package main

import (
	"fmt"
	"math"
	"sync"

	"github.com/jfreymuth/pulse"
	"github.com/jfreymuth/pulse/proto"
)

var (
	startSamples []int16
	endSamples   []int16
	soundOnce    sync.Once
)

func initSound() {
	// Start sound: snappy tick (fast decay, 200ms tail for PA buffer fill)
	startSamples = generateTick(44100, 1200, 0.2, 0.5, 60)
	// End sound: slightly lower tick (moderate decay, 200ms tail for PA buffer fill)
	endSamples = generateTick(44100, 900, 0.2, 0.5, 40)
}

func generateTick(sampleRate int, freq float64, duration float64, volume float64, decay float64) []int16 {
	n := int(float64(sampleRate) * duration)
	// Generate stereo (interleaved L/R) to match output sink format
	samples := make([]int16, n*2)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(sampleRate)
		envelope := math.Exp(-t * decay)
		s := int16(math.Sin(2*math.Pi*freq*t) * 32767 * volume * envelope)
		samples[i*2] = s   // left
		samples[i*2+1] = s // right
	}
	return samples
}

func playSamples(samples []int16) {
	if len(samples) == 0 {
		return
	}
	c, err := pulse.NewClient()
	if err != nil {
		logDiagError(fmt.Sprintf("pulse playback error: %v", err))
		return
	}
	defer c.Close()

	pos := 0
	reader := pulse.Int16Reader(func(buf []int16) (int, error) {
		if pos >= len(samples) {
			return 0, pulse.EndOfData
		}
		n := copy(buf, samples[pos:])
		pos += n
		return n, nil
	})
	stream, err := c.NewPlayback(reader,
		pulse.PlaybackStereo,
		pulse.PlaybackSampleRate(44100),
		pulse.PlaybackLatency(0.1),
		pulse.PlaybackRawOption(func(p *proto.CreatePlaybackStream) {
			p.ChannelVolumes = proto.ChannelVolumes{uint32(proto.VolumeNorm), uint32(proto.VolumeNorm)}
		}),
	)
	if err != nil {
		logDiagError(fmt.Sprintf("pulse playback error: %v", err))
		return
	}
	stream.Start()
	stream.Drain()
	stream.Stop()
	stream.Close()
}

func playStartSound() {
	soundOnce.Do(initSound)
	go playSamples(startSamples)
}

func playEndSound() {
	soundOnce.Do(initSound)
	go playSamples(endSamples)
}
