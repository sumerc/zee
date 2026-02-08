package encoder

import (
	"encoding/binary"
	"os"
	"testing"
)

func TestFlacEncoder(t *testing.T) {
	data, err := os.ReadFile("../test/data/short.wav")
	if err != nil {
		t.Skip("test/data/short.wav not found")
	}

	audioData := data[44:]
	samples := make([]int16, len(audioData)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(audioData[i*2:]))
	}

	enc, err := NewFlac()
	if err != nil {
		t.Fatalf("NewFlac: %v", err)
	}

	var totalFed uint64
	for i := 0; i < len(samples); i += BlockSize {
		end := i + BlockSize
		if end > len(samples) {
			end = len(samples)
		}
		block := samples[i:end]
		if err := enc.EncodeBlock(block); err != nil {
			t.Fatalf("EncodeBlock at offset %d: %v", i, err)
		}
		totalFed += uint64(len(block))
	}

	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if enc.TotalFrames() != totalFed {
		t.Errorf("TotalFrames = %d, want %d", enc.TotalFrames(), totalFed)
	}

	flacData := enc.Bytes()
	if len(flacData) < 4 || string(flacData[:4]) != "fLaC" {
		t.Fatal("output does not start with FLAC magic")
	}

	rawSize := len(samples) * 2
	t.Logf("Raw: %d bytes, FLAC: %d bytes (%.1f%% compression)",
		rawSize, len(flacData), (1-float64(len(flacData))/float64(rawSize))*100)
}

func TestFlacEncoderEmpty(t *testing.T) {
	enc, err := NewFlac()
	if err != nil {
		t.Fatalf("NewFlac: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close on empty encoder: %v", err)
	}
	if enc.TotalFrames() != 0 {
		t.Errorf("TotalFrames = %d, want 0", enc.TotalFrames())
	}
	if len(enc.Bytes()) == 0 {
		t.Error("expected non-empty FLAC output (at least header)")
	}
}

func TestFlacEncoderPartialBlock(t *testing.T) {
	enc, err := NewFlac()
	if err != nil {
		t.Fatalf("NewFlac: %v", err)
	}

	partial := make([]int16, BlockSize/4)
	for i := range partial {
		partial[i] = int16(i % 1000)
	}

	if err := enc.EncodeBlock(partial); err != nil {
		t.Fatalf("EncodeBlock partial: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if enc.TotalFrames() != uint64(len(partial)) {
		t.Errorf("TotalFrames = %d, want %d", enc.TotalFrames(), len(partial))
	}
}
