//go:build ignore

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
)

const (
	testSampleRate    = 16000
	testChannels      = 1
	testBitsPerSample = 16
	testBlockSize     = 4096
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run test_transcribe.go <wav_file>")
		os.Exit(1)
	}

	apiKey := os.Getenv("GROQ_API_KEY")
	if apiKey == "" {
		fmt.Println("Error: GROQ_API_KEY not set")
		os.Exit(1)
	}

	samples, err := readWAV(os.Args[1])
	if err != nil {
		fmt.Printf("Error reading WAV: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Read %d samples (%.2fs)\n", len(samples), float64(len(samples))/testSampleRate)

	encStart := time.Now()
	flacData, err := encodeToFLAC(samples)
	encTime := time.Since(encStart)
	if err != nil {
		fmt.Printf("Error encoding FLAC: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Encoded to %d bytes FLAC in %dms\n", len(flacData), encTime.Milliseconds())

	netStart := time.Now()
	text, remaining, err := transcribe(flacData, apiKey)
	netTime := time.Since(netStart)
	if err != nil {
		fmt.Printf("Error transcribing: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n✓ %q (%s req remaining)\n", text, remaining)
	fmt.Printf("  Network: %dms\n", netTime.Milliseconds())
}

func readWAV(path string) ([]int16, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	f.Seek(44, 0)

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	samples := make([]int16, len(data)/2)
	for i := 0; i < len(data); i += 2 {
		samples[i/2] = int16(binary.LittleEndian.Uint16(data[i:]))
	}
	return samples, nil
}

func encodeToFLAC(samples []int16) ([]byte, error) {
	var buf bytes.Buffer

	info := &meta.StreamInfo{
		BlockSizeMin:  testBlockSize,
		BlockSizeMax:  testBlockSize,
		SampleRate:    testSampleRate,
		NChannels:     testChannels,
		BitsPerSample: testBitsPerSample,
		NSamples:      uint64(len(samples)),
	}

	enc, err := flac.NewEncoder(&buf, info)
	if err != nil {
		return nil, err
	}

	for i := 0; i < len(samples); i += testBlockSize {
		end := i + testBlockSize
		if end > len(samples) {
			end = len(samples)
		}
		block := samples[i:end]

		samples32 := make([]int32, len(block))
		for j, s := range block {
			samples32[j] = int32(s)
		}

		subframe := &frame.Subframe{
			SubHeader: frame.SubHeader{
				Pred: frame.PredVerbatim,
			},
			Samples:  samples32,
			NSamples: len(block),
		}

		f := &frame.Frame{
			Header: frame.Header{
				BlockSize:     uint16(len(block)),
				SampleRate:    testSampleRate,
				Channels:      frame.ChannelsMono,
				BitsPerSample: testBitsPerSample,
			},
			Subframes: []*frame.Subframe{subframe},
		}

		if err := enc.WriteFrame(f); err != nil {
			return nil, err
		}
	}

	if err := enc.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func transcribe(audioData []byte, apiKey string) (string, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", "audio.flac")
	if err != nil {
		return "", "", err
	}
	if _, err := io.Copy(part, bytes.NewReader(audioData)); err != nil {
		return "", "", err
	}

	writer.WriteField("model", "whisper-large-v3-turbo")
	writer.WriteField("response_format", "text")
	writer.Close()

	req, err := http.NewRequest("POST", "https://api.groq.com/openai/v1/audio/transcriptions", &body)
	if err != nil {
		return "", "", err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	remaining := resp.Header.Get("x-ratelimit-remaining-requests")
	if remaining == "" {
		remaining = "?"
	}

	return string(respBody), remaining, nil
}
