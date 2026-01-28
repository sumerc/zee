//go:build !linux

package audio

import (
	"encoding/hex"
	"fmt"
	"sync/atomic"

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
			ID:   hex.EncodeToString(d.ID[:]),
			Name: d.Name(),
		})
	}
	return result, nil
}

func (m *malgoContext) NewCapture(device *DeviceInfo, config CaptureConfig) (CaptureDevice, error) {
	c := &malgoCapture{
		malgoCtx:   m,
		deviceInfo: device,
		config:     config,
	}

	if err := c.initDevice(); err != nil {
		return nil, err
	}

	return c, nil
}

func (m *malgoContext) Close() {
	m.ctx.Uninit()
	m.ctx.Free()
}

type malgoCapture struct {
	malgoCtx   *malgoContext
	deviceInfo *DeviceInfo
	config     CaptureConfig
	device     *malgo.Device
	callback   atomic.Pointer[DataCallback]
}

func (c *malgoCapture) initDevice() error {
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = c.config.Channels
	deviceConfig.SampleRate = c.config.SampleRate

	if c.deviceInfo != nil {
		idBytes, err := hex.DecodeString(c.deviceInfo.ID)
		if err != nil {
			return fmt.Errorf("invalid device ID: %w", err)
		}
		var devID malgo.DeviceID
		copy(devID[:], idBytes)
		deviceConfig.Capture.DeviceID = devID.Pointer()
	}

	callbacks := malgo.DeviceCallbacks{
		Data: func(_, data []byte, frameCount uint32) {
			if cb := c.callback.Load(); cb != nil {
				(*cb)(data, frameCount)
			}
		},
	}

	dev, err := malgo.InitDevice(c.malgoCtx.ctx.Context, deviceConfig, callbacks)
	if err != nil {
		return err
	}

	c.device = dev
	return nil
}

func (c *malgoCapture) Start() error {
	err := c.device.Start()
	if err == nil {
		return nil
	}

	// Device failed - try to recreate it (handles macOS sleep/wake)
	c.device.Uninit()
	if initErr := c.initDevice(); initErr != nil {
		return fmt.Errorf("device recovery failed: %w (original: %v)", initErr, err)
	}

	return c.device.Start()
}

func (c *malgoCapture) Stop() {
	c.device.Stop()
}

func (c *malgoCapture) Close() {
	c.device.Uninit()
}

func (c *malgoCapture) SetCallback(cb DataCallback) {
	c.callback.Store(&cb)
}

func (c *malgoCapture) ClearCallback() {
	c.callback.Store(nil)
}
