//go:build linux

package beep

import (
	"math"
	"sync"

	"github.com/jfreymuth/pulse"
	"github.com/jfreymuth/pulse/proto"
)

var (
	startSamples []int16
	endSamples   []int16
	errorSamples []int16
	soundOnce    sync.Once
)

func initSound() {
	startSamples = generateTick(sampleRate, startFreq, 0.2, startVolume, startDecay)
	endSamples = generateTick(sampleRate, endFreq, 0.2, endVolume, endDecay)
	errorSamples = generateDoubleBeep(sampleRate, errorFreq, 0.08, 0.05, errorVolume, errorDecay)
}

func generateTick(sampleRate int, freq float64, duration float64, volume float64, decay float64) []int16 {
	n := int(float64(sampleRate) * duration)
	samples := make([]int16, n*2)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(sampleRate)
		envelope := math.Exp(-t * decay)
		s := int16(math.Sin(2*math.Pi*freq*t) * 32767 * volume * envelope)
		samples[i*2] = s
		samples[i*2+1] = s
	}
	return samples
}

func generateDoubleBeep(sampleRate int, freq float64, beepDur float64, gapDur float64, volume float64, decay float64) []int16 {
	beep := generateTick(sampleRate, freq, beepDur, volume, decay)
	gap := make([]int16, int(float64(sampleRate)*gapDur)*2)
	result := make([]int16, 0, len(beep)*2+len(gap))
	result = append(result, beep...)
	result = append(result, gap...)
	result = append(result, beep...)
	return result
}

func playSamples(samples []int16) {
	if len(samples) == 0 {
		return
	}
	c, err := pulse.NewClient()
	if err != nil {
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
		pulse.PlaybackSampleRate(sampleRate),
		pulse.PlaybackLatency(0.1),
		pulse.PlaybackRawOption(func(p *proto.CreatePlaybackStream) {
			p.ChannelVolumes = proto.ChannelVolumes{uint32(proto.VolumeNorm), uint32(proto.VolumeNorm)}
		}),
	)
	if err != nil {
		return
	}
	stream.Start()
	stream.Drain()
	stream.Stop()
	stream.Close()
}

func Init() {
	soundOnce.Do(initSound)
}

func PlayStart() {
	soundOnce.Do(initSound)
	go playSamples(startSamples)
}

func PlayEnd() {
	soundOnce.Do(initSound)
	go playSamples(endSamples)
}

func PlayError() {
	soundOnce.Do(initSound)
	go playSamples(errorSamples)
}
