package audio

import "strings"

const WAVHeaderSize = 44

var btKeywords = []string{
	"airpods", "beats", "bose", "wh-1000", "wf-1000",
	"sony wh-", "sony wf-",
	"jabra", "galaxy buds", "pixel buds", "powerbeats",
	"jbl ", "sennheiser momentum", "plantronics",
	"tozo", "anker soundcore", "skullcandy",
	"bluetooth", " bt ", " bt)", " bt]",
}

func IsBluetooth(name string) bool {
	lower := strings.ToLower(name)
	for _, kw := range btKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

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
	NewCapture(device *DeviceInfo, config CaptureConfig) (CaptureDevice, error)
	Close()
}

type CaptureDevice interface {
	Start() error
	Stop()
	Close()
	SetCallback(cb DataCallback)
	ClearCallback()
}
