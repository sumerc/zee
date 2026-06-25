//go:build !linux

package audio

import (
	"encoding/hex"
	"fmt"
	"sync/atomic"

	"github.com/gen2brain/malgo"

	"zee/internal/malgolock"
)

type malgoContext struct {
	ctx *malgo.AllocatedContext
}

func NewContext() (Context, error) {
	malgolock.Lock()
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	malgolock.Unlock()
	if err != nil {
		return nil, err
	}
	return &malgoContext{ctx: ctx}, nil
}

func (m *malgoContext) Devices() ([]DeviceInfo, error) {
	malgolock.Lock()
	devices, err := m.ctx.Devices(malgo.Capture)
	malgolock.Unlock()
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

	malgolock.Lock()
	err := c.initDevice()
	malgolock.Unlock()
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (m *malgoContext) Close() {
	malgolock.Lock()
	m.ctx.Uninit()
	m.ctx.Free()
	malgolock.Unlock()
}

type malgoCapture struct {
	malgoCtx   *malgoContext
	deviceInfo *DeviceInfo
	config     CaptureConfig
	device     *malgo.Device
	callback   atomic.Pointer[DataCallback]
}

// initDevice is lock-free; callers must hold malgolock around it.
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
	malgolock.Lock()
	defer malgolock.Unlock()
	// Always reinitialize before starting — handles macOS sleep/wake where the
	// device handle goes stale without returning errors. Null the pointer after
	// Uninit: if the reinit below fails (transient CoreAudio error during a
	// route/sleep-wake change), c.device must not be left pointing at the freed
	// device, or the next Start uninits it again and double-frees.
	if c.device != nil {
		c.device.Uninit()
		c.device = nil
	}
	if err := c.initDevice(); err != nil {
		return fmt.Errorf("device reinit failed: %w", err)
	}
	return c.device.Start()
}

func (c *malgoCapture) Stop() {
	malgolock.Lock()
	if c.device != nil {
		c.device.Stop()
	}
	malgolock.Unlock()
}

func (c *malgoCapture) Close() {
	malgolock.Lock()
	if c.device != nil {
		c.device.Uninit()
		c.device = nil
	}
	malgolock.Unlock()
}

func (c *malgoCapture) SetCallback(cb DataCallback) {
	c.callback.Store(&cb)
}

func (c *malgoCapture) ClearCallback() {
	c.callback.Store(nil)
}

func (c *malgoCapture) DeviceName() string {
	if c.deviceInfo != nil {
		return c.deviceInfo.Name
	}
	return "system default"
}
