package audio

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
)

const WAVHeaderSize = 44

// WAVToPCM parses a RIFF/WAVE file and returns the raw PCM data chunk, after
// validating it is 16 kHz mono signed-16-bit little-endian — the format the
// local Parakeet engine expects. It walks the chunk list so padding chunks
// (FLLR, LIST, fact, …) between the header and `data` are handled correctly.
//
// Use case: the `-transcribe <file.wav>` flow (main.go transcribeFile), which
// feeds a WAV from disk to a local transcriber. Live recording captures raw PCM
// and never hits this. In practice that flow is driven mostly by the integration
// tests (test/integration_test.go transcribeFiles), so this is largely test-path
// code, not the hot path.
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

// PCMToF32 converts raw signed-16-bit little-endian PCM (the capture format,
// and what WAVToPCM returns) to the -1..1 float32 samples the local Parakeet
// engine consumes. A trailing odd byte is ignored.
func PCMToF32(pcm []byte) []float32 {
	f32 := make([]float32, len(pcm)/2)
	for i := range f32 {
		f32[i] = float32(int16(binary.LittleEndian.Uint16(pcm[i*2:]))) / 32768.0
	}
	return f32
}

// PCMToWAV wraps raw 16 kHz mono signed-16-bit little-endian PCM in a minimal
// 44-byte RIFF/WAVE container — the inverse of WAVToPCM.
//
// Use case: the live local-transcriber (Parakeet) path. API providers encode
// captured audio to mp3/flac, but Parakeet consumes raw PCM and never encodes,
// so to hand back a real, saveable file in SessionResult.AudioData it gets
// wrapped here. That's what the "Save Last Recording" feature (and the
// auto-save-on-error path in main.go) writes to disk. Unlike WAVToPCM, this is
// on the hot path, not test-only.
// PCMToWAV wraps raw 16 kHz mono S16 PCM (the capture format) in a WAV header.
func PCMToWAV(pcm []byte) []byte { return pcmWAV(pcm, 16000) }

func pcmWAV(pcm []byte, sampleRate int) []byte {
	const channels, bits = 1, 16
	byteRate := sampleRate * channels * bits / 8
	blockAlign := channels * bits / 8

	buf := make([]byte, WAVHeaderSize+len(pcm))
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+len(pcm)))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16) // PCM fmt chunk size
	binary.LittleEndian.PutUint16(buf[20:22], 1)  // format = PCM
	binary.LittleEndian.PutUint16(buf[22:24], channels)
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(buf[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(buf[34:36], bits)
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(len(pcm)))
	copy(buf[WAVHeaderSize:], pcm)
	return buf
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

// --- Feedback tones (record start/end, error, denied) ---
//
// This file owns the platform-neutral half: the public API, the enable guard,
// and tone synthesis. Each platform file provides exactly two backend hooks,
// initSound() and playOne(sound), so the guard logic lives here once instead
// of per platform. Playback is independent of capture on every OS (darwin:
// AVAudioPlayer, linux: PulseAudio) — it never touches the capture device.

// sound identifies a tone; it indexes the canonical sample table below.
type sound int

const (
	startSound sound = iota
	endSound
	errorSound
	deniedSound
	numSounds // count sentinel; sizes the sample table
)

// samples is the canonical tone table: 16-bit mono PCM, synthesized once by
// buildSamples. Each platform's playback adapts these to its own format
// (darwin: in-memory WAV bytes; linux: stereo stream) — so the tone data and
// synthesis live here once, never per OS.
var samples [numSounds][]int16

// disabled is set once at startup (or by tests) but read from recording
// goroutines, so it's atomic to stay race-free.
var disabled atomic.Bool

// soundOnce guards the one-time device + buffer init done by initSound.
var soundOnce sync.Once

// DisableBeep silences all feedback tones (used by test mode).
func DisableBeep() { disabled.Store(true) }

// InitBeep eagerly performs the one-time setup so the first real beep isn't delayed.
func InitBeep() { soundOnce.Do(initSound) }

func PlayStart()  { play(startSound) }
func PlayEnd()    { play(endSound) }
func PlayError()  { play(errorSound) }
func PlayDenied() { play(deniedSound) }

func play(s sound) {
	if disabled.Load() {
		return
	}
	soundOnce.Do(initSound)
	playOne(s)
}

const (
	beepSampleRate = 44100

	// Start beep: high pitch, short
	startFreq   = 1200
	startVolume = 0.5
	startDecay  = 60

	// End beep: medium pitch, slightly longer
	endFreq   = 900
	endVolume = 0.5
	endDecay  = 40

	// Error beep: low pitch double-beep
	errorFreq   = 350
	errorVolume = 0.6
	errorDecay  = 30

	// Denied beep: low, short single tick — a press was ignored (e.g. while a
	// transcription is still in progress).
	deniedFreq   = 240
	deniedVolume = 0.45
	deniedDecay  = 40
)

// generateTick synthesizes a single decaying sine tone as 16-bit mono PCM.
func generateTick(freq, duration, volume, decay float64) []int16 {
	n := int(float64(beepSampleRate) * duration)
	buf := make([]int16, n)
	for i := range n {
		t := float64(i) / float64(beepSampleRate)
		env := math.Exp(-t * decay)
		buf[i] = int16(math.Sin(2*math.Pi*freq*t) * 32767 * volume * env)
	}
	return buf
}

// generateDoubleBeep is two ticks separated by a silent gap (used for errors).
func generateDoubleBeep(freq, beepDur, gapDur, volume, decay float64) []int16 {
	beep := generateTick(freq, beepDur, volume, decay)
	gap := make([]int16, int(float64(beepSampleRate)*gapDur))
	out := make([]int16, 0, len(beep)*2+len(gap))
	out = append(out, beep...)
	out = append(out, gap...)
	out = append(out, beep...)
	return out
}

// buildSamples synthesizes the canonical tone table. Durations are shared
// across platforms so the feedback sounds are identical on every OS.
func buildSamples() {
	samples[startSound] = generateTick(startFreq, 0.2, startVolume, startDecay)
	samples[endSound] = generateTick(endFreq, 0.2, endVolume, endDecay)
	samples[errorSound] = generateDoubleBeep(errorFreq, 0.08, 0.05, errorVolume, errorDecay)
	samples[deniedSound] = generateTick(deniedFreq, 0.12, deniedVolume, deniedDecay)
}
