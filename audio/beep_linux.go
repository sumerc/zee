//go:build linux

package audio

import (
	"github.com/jfreymuth/pulse"
	"github.com/jfreymuth/pulse/proto"
)

func initSound() {
	buildSamples()
}

// playSamples plays a canonical mono buffer through PulseAudio, duplicating each
// sample to both channels of the stereo stream as it fills the read buffer.
func playSamples(mono []int16) {
	if len(mono) == 0 {
		return
	}
	c, err := pulse.NewClient()
	if err != nil {
		return
	}
	defer c.Close()

	pos := 0
	reader := pulse.Int16Reader(func(buf []int16) (int, error) {
		if pos >= len(mono) {
			return 0, pulse.EndOfData
		}
		n := 0
		for n+1 < len(buf) && pos < len(mono) {
			buf[n] = mono[pos]   // left
			buf[n+1] = mono[pos] // right
			n += 2
			pos++
		}
		return n, nil
	})
	stream, err := c.NewPlayback(reader,
		pulse.PlaybackStereo,
		pulse.PlaybackSampleRate(beepSampleRate),
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

func playOne(s sound) {
	go playSamples(samples[s])
}
