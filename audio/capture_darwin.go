//go:build darwin

package audio

import (
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"

	"zee/log"
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
	// Timing breakdown of the record-start hot path: the mic device is torn down
	// and re-created on every Start (see below), and this whole block holds
	// deviceMu, so it can stall the beep and — empirically — delay hotkey event
	// delivery. capture_start_ms is the dominant term in press_to_record_ms.
	t0 := time.Now()
	deviceMu.Lock()
	waitMs := time.Since(t0).Milliseconds()
	defer deviceMu.Unlock()
	// EXPERIMENT(tap-misfire): reuse the initialized device instead of the
	// per-press Uninit+InitDevice (which existed to survive macOS sleep/wake
	// staleness). Tests whether the reinit is what stalls the run loop and
	// delays keyup delivery. uninit_ms should now always be 0 and init_ms ~0
	// after the first press.
	var uninitMs int64
	ti := time.Now()
	if c.device == nil {
		if err := c.initDevice(); err != nil {
			return fmt.Errorf("device reinit failed: %w", err)
		}
	}
	initMs := time.Since(ti).Milliseconds()
	ts := time.Now()
	err := c.device.Start()
	log.Info(fmt.Sprintf("capture_start devicemu_wait_ms=%d uninit_ms=%d init_ms=%d start_ms=%d",
		waitMs, uninitMs, initMs, time.Since(ts).Milliseconds()))
	return err
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
