package audio

import (
	"encoding/binary"
	"fmt"
	"strings"
)

const WAVHeaderSize = 44

// WAVToPCM parses a RIFF/WAVE file and returns the raw PCM data chunk, after
// validating it is 16 kHz mono signed-16-bit little-endian — the format the
// local Parakeet engine expects. It walks the chunk list so padding chunks
// (FLLR, LIST, fact, …) between the header and `data` are handled correctly.
func WAVToPCM(b []byte) ([]byte, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a RIFF/WAVE file")
	}
	var (
		gotFmt         bool
		channels, bits uint16
		sampleRate     uint32
		data           []byte
	)
	for off := 12; off+8 <= len(b); {
		id := string(b[off : off+4])
		size := int(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		body := off + 8
		if body+size > len(b) {
			size = len(b) - body // tolerate a truncated final chunk
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, fmt.Errorf("short fmt chunk")
			}
			channels = binary.LittleEndian.Uint16(b[body+2 : body+4])
			sampleRate = binary.LittleEndian.Uint32(b[body+4 : body+8])
			bits = binary.LittleEndian.Uint16(b[body+14 : body+16])
			gotFmt = true
		case "data":
			data = b[body : body+size]
		}
		off = body + size
		if size%2 == 1 {
			off++ // chunks are word-aligned
		}
	}
	if !gotFmt {
		return nil, fmt.Errorf("no fmt chunk")
	}
	if data == nil {
		return nil, fmt.Errorf("no data chunk")
	}
	if channels != 1 || sampleRate != 16000 || bits != 16 {
		return nil, fmt.Errorf("unsupported WAV format: %d-bit %d ch %d Hz (need 16-bit mono 16000 Hz)", bits, channels, sampleRate)
	}
	return data, nil
}

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
	DeviceName() string
}
