//go:build linux

package audio

import (
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/jfreymuth/pulse"
)

const nativeSampleRate = 44100

type pulseContext struct {
	client *pulse.Client
}

func NewContext() (Context, error) {
	c, err := pulse.NewClient()
	if err != nil {
		return nil, fmt.Errorf("pulse: %w", err)
	}
	return &pulseContext{client: c}, nil
}

func (p *pulseContext) Devices() ([]DeviceInfo, error) {
	sources, err := p.client.ListSources()
	if err != nil {
		return nil, fmt.Errorf("pulse list sources: %w", err)
	}
	var devices []DeviceInfo
	for _, s := range sources {
		devices = append(devices, DeviceInfo{
			ID:   s.ID(),
			Name: s.Name(),
		})
	}
	return devices, nil
}

func (p *pulseContext) NewCapture(device *DeviceInfo, config CaptureConfig, callback DataCallback) (CaptureDevice, error) {
	return &pulseCapture{
		client:   p.client,
		device:   device,
		config:   config,
		callback: callback,
	}, nil
}

func (p *pulseContext) Close() {
	p.client.Close()
}

type pulseCapture struct {
	client   *pulse.Client
	device   *DeviceInfo
	config   CaptureConfig
	callback DataCallback

	stream *pulse.RecordStream
	mu     sync.Mutex
	stop   chan struct{}
	done   chan struct{}
}

func (c *pulseCapture) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	targetRate := c.config.SampleRate
	ratio := float64(nativeSampleRate) / float64(targetRate)
	var pos float64

	writer := pulse.Int16Writer(func(buf []int16) (int, error) {
		// Downmix stereo to mono
		monoLen := len(buf) / 2
		mono := make([]int16, monoLen)
		for i := 0; i < monoLen; i++ {
			l := int32(buf[i*2])
			r := int32(buf[i*2+1])
			mono[i] = int16((l + r) / 2)
		}

		// Downsample from nativeSampleRate to targetRate
		var resampled []int16
		for pos < float64(monoLen) {
			idx := int(pos)
			if idx >= monoLen {
				break
			}
			resampled = append(resampled, mono[idx])
			pos += ratio
		}
		pos -= float64(monoLen)

		if len(resampled) == 0 {
			return len(buf), nil
		}

		// Convert to little-endian bytes
		data := make([]byte, len(resampled)*2)
		for i, s := range resampled {
			binary.LittleEndian.PutUint16(data[i*2:], uint16(s))
		}
		c.callback(data, uint32(len(resampled)))
		return len(buf), nil
	})

	opts := []pulse.RecordOption{
		pulse.RecordSampleRate(nativeSampleRate),
		pulse.RecordStereo,
	}
	if c.device != nil {
		source, err := c.client.SourceByID(c.device.ID)
		if err == nil && source != nil {
			opts = append(opts, pulse.RecordSource(source))
		}
	}

	stream, err := c.client.NewRecord(writer, opts...)
	if err != nil {
		return fmt.Errorf("pulse record: %w", err)
	}

	c.stream = stream
	c.stop = make(chan struct{})
	c.done = make(chan struct{})

	go func() {
		defer close(c.done)
		stream.Start()
		<-c.stop
		stream.Stop()
	}()

	return nil
}

func (c *pulseCapture) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stop != nil {
		select {
		case <-c.stop:
		default:
			close(c.stop)
		}
		<-c.done
	}
}

func (c *pulseCapture) Close() {
	c.Stop()
}
