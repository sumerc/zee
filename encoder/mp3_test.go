package encoder

import (
	"encoding/binary"
	"os"
	"testing"
)

func TestMp3EncoderMono(t *testing.T) {
	data, err := os.ReadFile("../test/data/short.wav")
	if err != nil {
		t.Skip("test/data/short.wav not found")
	}

	audioData := data[44:]
	samples := make([]int16, len(audioData)/2)
	for i := 0; i < len(samples); i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(audioData[i*2:]))
	}

	enc, err := NewMp3(32)
	if err != nil {
		t.Fatalf("NewMp3: %v", err)
	}

	for i := 0; i < len(samples); i += BlockSize {
		end := i + BlockSize
		if end > len(samples) {
			end = len(samples)
		}
		enc.EncodeBlock(samples[i:end])
	}
	enc.Close()

	mp3Data := enc.Bytes()
	rawSize := len(samples) * 2

	t.Logf("Raw: %d bytes, MP3: %d bytes (%.1f%% compression)",
		rawSize, len(mp3Data), (1-float64(len(mp3Data))/float64(rawSize))*100)

	durationSec := float64(len(samples)) / 16000
	expectedMax := int(durationSec * 20000)
	if len(mp3Data) > expectedMax {
		t.Errorf("MP3 too large: %d bytes (expected < %d for %.1fs)", len(mp3Data), expectedMax, durationSec)
	}

	if len(mp3Data) < 2 || mp3Data[0] != 0xff || (mp3Data[1]&0xe0) != 0xe0 {
		t.Errorf("Invalid MP3 header: %x %x", mp3Data[0], mp3Data[1])
	}
}
