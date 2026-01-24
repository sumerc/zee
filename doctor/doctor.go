package doctor

import (
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"ses9000/audio"
	"ses9000/encoder"
	"ses9000/shortcut"
	"ses9000/transcriber"
)

// Run executes all diagnostic checks and returns an exit code (0=all pass, 1=any fail).
func Run(wavFile string) int {
	fmt.Println("ses9000 doctor - system diagnostics")
	fmt.Println("========================================")

	allPass := true

	if !checkHotkey() {
		allPass = false
	}
	if !checkAudio() {
		allPass = false
	}
	if !checkEncoder() {
		allPass = false
	}
	if !checkTranscription(wavFile) {
		allPass = false
	}
	if !checkClipboardCopy() {
		allPass = false
	}
	if !checkClipboardPaste() {
		allPass = false
	}

	fmt.Println()
	if allPass {
		fmt.Println("All checks passed.")
	} else {
		fmt.Println("Some checks failed. See details above.")
	}

	if allPass {
		return 0
	}
	return 1
}

func checkHotkey() bool {
	fmt.Println()
	fmt.Println("[1/6] Hotkey access")

	msg, err := shortcut.Diagnose()
	if err != nil {
		fmt.Printf("  FAIL: %v\n", err)
		return false
	}

	fmt.Printf("  PASS: %s\n", msg)
	return true
}

func checkAudio() bool {
	fmt.Println()
	fmt.Println("[2/6] Audio capture")

	ctx, err := audio.NewContext()
	if err != nil {
		fmt.Printf("  FAIL: cannot connect to audio: %v\n", err)
		return false
	}
	defer ctx.Close()

	devices, err := ctx.Devices()
	if err != nil {
		fmt.Printf("  FAIL: cannot list audio devices: %v\n", err)
		return false
	}

	if len(devices) == 0 {
		fmt.Println("  FAIL: no audio capture sources found")
		return false
	}

	names := make([]string, len(devices))
	for i, d := range devices {
		names[i] = d.Name
	}
	fmt.Printf("  PASS: %d source(s): %s\n", len(devices), strings.Join(names, ", "))
	return true
}

func checkEncoder() bool {
	fmt.Println()
	fmt.Println("[3/6] Encoder (MP3 + FLAC)")

	// Generate 0.5s synthetic 440Hz tone at 16kHz mono int16
	numSamples := encoder.SampleRate / 2 // 0.5 seconds
	samples := make([]int16, numSamples)
	for i := range samples {
		t := float64(i) / float64(encoder.SampleRate)
		samples[i] = int16(math.Sin(2*math.Pi*440*t) * 16000)
	}
	rawSize := numSamples * 2 // 2 bytes per sample

	// Encode MP3
	mp3Enc, err := encoder.NewMp3(64)
	if err != nil {
		fmt.Printf("  FAIL: MP3 encoder init: %v\n", err)
		return false
	}
	for i := 0; i < len(samples); i += encoder.BlockSize {
		end := i + encoder.BlockSize
		if end > len(samples) {
			end = len(samples)
		}
		if err := mp3Enc.EncodeBlock(samples[i:end]); err != nil {
			fmt.Printf("  FAIL: MP3 encode: %v\n", err)
			return false
		}
	}
	if err := mp3Enc.Close(); err != nil {
		fmt.Printf("  FAIL: MP3 close: %v\n", err)
		return false
	}
	mp3Size := len(mp3Enc.Bytes())

	// Encode FLAC
	flacEnc, err := encoder.NewFlac()
	if err != nil {
		fmt.Printf("  FAIL: FLAC encoder init: %v\n", err)
		return false
	}
	for i := 0; i < len(samples); i += encoder.BlockSize {
		end := i + encoder.BlockSize
		if end > len(samples) {
			end = len(samples)
		}
		if err := flacEnc.EncodeBlock(samples[i:end]); err != nil {
			fmt.Printf("  FAIL: FLAC encode: %v\n", err)
			return false
		}
	}
	if err := flacEnc.Close(); err != nil {
		fmt.Printf("  FAIL: FLAC close: %v\n", err)
		return false
	}
	flacSize := len(flacEnc.Bytes())

	if mp3Size == 0 {
		fmt.Println("  FAIL: MP3 produced empty output")
		return false
	}
	if flacSize == 0 {
		fmt.Println("  FAIL: FLAC produced empty output")
		return false
	}

	mp3Pct := (1.0 - float64(mp3Size)/float64(rawSize)) * 100
	flacPct := (1.0 - float64(flacSize)/float64(rawSize)) * 100
	fmt.Printf("  PASS: MP3: %dB (%.0f%%), FLAC: %dB (%.0f%%) from %dB raw\n",
		mp3Size, mp3Pct, flacSize, flacPct, rawSize)
	return true
}

func checkTranscription(wavFile string) bool {
	fmt.Println()
	fmt.Println("[4/6] Transcription API")

	dgKey := os.Getenv("DEEPGRAM_API_KEY")
	groqKey := os.Getenv("GROQ_API_KEY")

	if dgKey == "" && groqKey == "" {
		fmt.Println("  FAIL: neither DEEPGRAM_API_KEY nor GROQ_API_KEY is set")
		return false
	}

	var provider string
	var apiURL string
	if dgKey != "" {
		provider = "deepgram"
		apiURL = "https://api.deepgram.com"
	}
	if groqKey != "" {
		provider = "groq"
		apiURL = "https://api.groq.com"
	}

	if wavFile == "" {
		// Connectivity-only test: HEAD request with timing
		start := time.Now()
		req, _ := http.NewRequest("HEAD", apiURL, nil)
		resp, err := http.DefaultClient.Do(req)
		elapsed := time.Since(start)
		if err != nil {
			fmt.Printf("  FAIL: provider=%s, connectivity error: %v\n", provider, err)
			return false
		}
		resp.Body.Close()
		fmt.Printf("  PASS: provider=%s, connectivity OK (%dms)\n", provider, elapsed.Milliseconds())
		return true
	}

	// Full transcription test with WAV file
	data, err := os.ReadFile(wavFile)
	if err != nil {
		fmt.Printf("  FAIL: cannot read WAV file: %v\n", err)
		return false
	}
	if len(data) < 44 {
		fmt.Println("  FAIL: invalid WAV file (too small)")
		return false
	}

	// Decode WAV samples
	audioData := data[44:]
	samples := make([]int16, len(audioData)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(audioData[i*2:]))
	}

	// Encode to MP3
	mp3Enc, err := encoder.NewMp3(64)
	if err != nil {
		fmt.Printf("  FAIL: encoder init: %v\n", err)
		return false
	}
	for i := 0; i < len(samples); i += encoder.BlockSize {
		end := i + encoder.BlockSize
		if end > len(samples) {
			end = len(samples)
		}
		if err := mp3Enc.EncodeBlock(samples[i:end]); err != nil {
			fmt.Printf("  FAIL: encode: %v\n", err)
			return false
		}
	}
	if err := mp3Enc.Close(); err != nil {
		fmt.Printf("  FAIL: encode close: %v\n", err)
		return false
	}

	// Transcribe
	t := transcriber.New()
	start := time.Now()
	result, err := t.Transcribe(mp3Enc.Bytes(), "mp3")
	elapsed := time.Since(start)
	if err != nil {
		fmt.Printf("  FAIL: transcription error: %v\n", err)
		return false
	}

	text := strings.TrimSpace(result.Text)
	if text == "" {
		text = "(no speech detected)"
	}
	fmt.Printf("  PASS: provider=%s, transcribed in %dms\n", provider, elapsed.Milliseconds())
	fmt.Printf("  Text: %s\n", text)
	return true
}

