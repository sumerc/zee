//go:build darwin

package audio

import (
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/gen2brain/malgo"
)

// deviceMu serializes every malgo context/device lifecycle call across capture
// AND playback (beep_darwin.go). miniaudio's CoreAudio backend keeps process-
// global default-device state that ma_device_init/uninit/start/stop mutate; two
// threads touching it concurrently corrupt the heap (observed SIGSEGV in
// ma_device_uninit). It does NOT guard the audio data callbacks — those run on
// miniaudio's own thread and don't touch lifecycle state. Non-reentrant:
// acquire once per logical operation, never nest.
var deviceMu sync.Mutex

// maCtx is the single process-wide malgo context, shared by capture and
// playback so there's one piece of CoreAudio global state, not two. Created
// lazily under deviceMu and never freed — its lifetime is the process.
var maCtx *malgo.AllocatedContext

// ensureContext creates the shared context on first use. Caller must hold deviceMu.
func ensureContext() error {
	if maCtx != nil {
		return nil
	}
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return err
	}
	maCtx = ctx
	return nil
}

type malgoContext struct{}

func NewContext() (Context, error) {
	deviceMu.Lock()
	defer deviceMu.Unlock()
	if err := ensureContext(); err != nil {
		return nil, err
	}
	return &malgoContext{}, nil
}

func (m *malgoContext) Devices() ([]DeviceInfo, error) {
	deviceMu.Lock()
	devices, err := maCtx.Devices(malgo.Capture)
	deviceMu.Unlock()
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
		deviceInfo: device,
		config:     config,
	}

	deviceMu.Lock()
	err := c.initDevice()
	deviceMu.Unlock()
	if err != nil {
		return nil, err
	}

	return c, nil
}

// Close is a no-op: the shared malgo context is process-wide and outlives any
// single Context (playback may still need it). Capture devices are torn down
// individually via malgoCapture.Close.
func (m *malgoContext) Close() {}

type malgoCapture struct {
	deviceInfo *DeviceInfo
	config     CaptureConfig
	device     *malgo.Device
	callback   atomic.Pointer[DataCallback]
}

// initDevice is lock-free; callers must hold deviceMu around it.
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

	dev, err := malgo.InitDevice(maCtx.Context, deviceConfig, callbacks)
	if err != nil {
		return err
	}

	c.device = dev
	return nil
}

func (c *malgoCapture) Start() error {
	deviceMu.Lock()
	defer deviceMu.Unlock()
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
	deviceMu.Lock()
	if c.device != nil {
		c.device.Stop()
	}
	deviceMu.Unlock()
}

func (c *malgoCapture) Close() {
	deviceMu.Lock()
	if c.device != nil {
		c.device.Uninit()
		c.device = nil
	}
	deviceMu.Unlock()
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
