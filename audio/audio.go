package audio

type DataCallback func(data []byte, frameCount uint32)

type CaptureConfig struct {
	SampleRate uint32
	Channels   uint32
}

type DeviceInfo struct {
	ID   string // opaque platform-specific identifier
	Name string
}

type Context interface {
	Devices() ([]DeviceInfo, error)
	NewCapture(device *DeviceInfo, config CaptureConfig, callback DataCallback) (CaptureDevice, error)
	Close()
}

type CaptureDevice interface {
	Start() error
	Stop()
	Close()
}
