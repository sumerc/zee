//go:build !linux

package audio

import (
	"encoding/hex"
	"fmt"

	"github.com/gen2brain/malgo"
)

type malgoContext struct {
	ctx *malgo.AllocatedContext
}

func NewContext() (Context, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, err
	}
	return &malgoContext{ctx: ctx}, nil
}

func (m *malgoContext) Devices() ([]DeviceInfo, error) {
	devices, err := m.ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("malgo devices: %w", err)
	}
	var result []DeviceInfo
	for _, d := range devices {
		result = append(result, DeviceInfo{
			ID:   hex.EncodeToString(d.ID.Pointer()[:]),
			Name: d.Name(),
		})
	}
	return result, nil
}

func (m *malgoContext) NewCapture(device *DeviceInfo, config CaptureConfig, callback DataCallback) (CaptureDevice, error) {
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = config.Channels
	deviceConfig.SampleRate = config.SampleRate

	if device != nil {
		idBytes, err := hex.DecodeString(device.ID)
		if err != nil {
			return nil, fmt.Errorf("invalid device ID: %w", err)
		}
		var devID malgo.DeviceID
		copy(devID[:], idBytes)
		deviceConfig.Capture.DeviceID = devID.Pointer()
	}

	callbacks := malgo.DeviceCallbacks{
		Data: func(_, data []byte, frameCount uint32) {
			callback(data, frameCount)
		},
	}

	dev, err := malgo.InitDevice(m.ctx.Context, deviceConfig, callbacks)
	if err != nil {
		return nil, err
	}

	return &malgoCapture{device: dev}, nil
}

func (m *malgoContext) Close() {
	m.ctx.Uninit()
	m.ctx.Free()
}

type malgoCapture struct {
	device *malgo.Device
}

func (c *malgoCapture) Start() error {
	return c.device.Start()
}

func (c *malgoCapture) Stop() {
	c.device.Stop()
}

func (c *malgoCapture) Close() {
	c.device.Uninit()
}
