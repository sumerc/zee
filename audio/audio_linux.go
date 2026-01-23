//go:build linux

package audio

import (
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/jfreymuth/pulse"
)


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

	const gain = 4 // software gain to compensate for weak mic input

	writer := pulse.Int16Writer(func(buf []int16) (int, error) {
		if len(buf) == 0 {
			return 0, nil
		}
		data := make([]byte, len(buf)*2)
		for i, s := range buf {
			amplified := int32(s) * gain
			if amplified > 32767 {
				amplified = 32767
			} else if amplified < -32768 {
				amplified = -32768
			}
			binary.LittleEndian.PutUint16(data[i*2:], uint16(int16(amplified)))
		}
		c.callback(data, uint32(len(buf)))
		return len(buf), nil
	})

	opts := []pulse.RecordOption{
		pulse.RecordMono,
		pulse.RecordSampleRate(int(c.config.SampleRate)),
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
		stream.Close()
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
