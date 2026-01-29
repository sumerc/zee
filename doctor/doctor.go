package doctor

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"zee/audio"
	"zee/clipboard"
	"zee/encoder"
	"zee/hotkey"
	"zee/transcriber"
)

// Run executes interactive diagnostic checks and returns an exit code (0=all pass, 1=any fail).
func Run(_ string) int {
	resetTerminal()
	setupInterruptHandler()

	fmt.Println("zee doctor - interactive system diagnostics")
	fmt.Println("============================================")

	allPass := true

	if !checkHotkey() {
		allPass = false
	}
	if allPass && !checkMicAndTranscription() {
		allPass = false
	}
	if allPass && !checkClipboard() {
		allPass = false
	}

	fmt.Println()
	if allPass {
		fmt.Println("All checks passed!")
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
	fmt.Println("[1/3] Hotkey detection")
	fmt.Println("Press Ctrl+Shift+Space...")

	hk := hotkey.New()
	if err := hk.Register(); err != nil {
		fmt.Printf("  FAIL: could not register hotkey: %v\n", err)
		return false
	}
	defer hk.Unregister()

	select {
	case <-hk.Keydown():
		fmt.Println("  PASS: hotkey detected")
		// Wait for keyup to avoid triggering next step
		select {
		case <-hk.Keyup():
		case <-time.After(5 * time.Second):
		}
		// Reset terminal after hotkey - it may leave terminal in raw mode
		resetTerminal()
		return true
	case <-time.After(10 * time.Second):
		fmt.Println("  FAIL: timeout waiting for hotkey")
		return false
	}
}

func checkMicAndTranscription() bool {
	fmt.Println()
	fmt.Println("[2/3] Microphone and transcription")

	reader := bufio.NewReader(os.Stdin)

	// Init audio context
	ctx, err := audio.NewContext()
	if err != nil {
		fmt.Printf("  FAIL: cannot connect to audio: %v\n", err)
		return false
	}
	defer ctx.Close()

	// Select device
	devices, err := ctx.Devices()
	if err != nil {
		fmt.Printf("  FAIL: cannot list devices: %v\n", err)
		return false
	}
	if len(devices) == 0 {
		fmt.Println("  FAIL: no capture devices found")
		return false
	}

	var device *audio.DeviceInfo
	if len(devices) == 1 {
		device = &devices[0]
		fmt.Printf("Using device: %s\n", device.Name)
	} else {
		fmt.Println()
		fmt.Println("Select input device:")
		for i, d := range devices {
			fmt.Printf("  %d. %s\n", i+1, d.Name)
		}
		fmt.Printf("Choice [1-%d]: ", len(devices))

		devChoice, _ := reader.ReadString('\n')
		devChoice = strings.TrimSpace(devChoice)
		idx := 0
		if devChoice == "" {
			idx = 0
		} else {
			fmt.Sscanf(devChoice, "%d", &idx)
			idx--
		}
		if idx < 0 || idx >= len(devices) {
			fmt.Printf("  FAIL: invalid choice\n")
			return false
		}
		device = &devices[idx]
		fmt.Printf("Selected: %s\n", device.Name)
	}

	// Select provider
	fmt.Println()
	fmt.Println("Select transcription provider:")
	fmt.Println("  1. Groq")
	fmt.Println("  2. DeepGram")
	fmt.Print("Choice [1/2]: ")

	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	var provider string
	switch choice {
	case "1", "":
		provider = "groq"
	case "2":
		provider = "deepgram"
	default:
		fmt.Printf("  FAIL: invalid choice %q\n", choice)
		return false
	}

	// Get API key
	fmt.Printf("Enter %s API key: ", provider)
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		fmt.Println("  FAIL: API key required")
		return false
	}

	// Create transcriber
	var trans transcriber.Transcriber
	switch provider {
	case "groq":
		trans = transcriber.NewGroq(apiKey)
	case "deepgram":
		trans = transcriber.NewDeepgram(apiKey)
	}

	fmt.Println()
	fmt.Print("Press Enter and speak for 3 seconds...")
	reader.ReadString('\n')

	// Record for 3 seconds
	stop := make(chan struct{})
	go func() {
		time.Sleep(3 * time.Second)
		close(stop)
	}()

	audioData, err := recordAudio(ctx, device, stop)
	if err != nil {
		fmt.Printf("  FAIL: recording error: %v\n", err)
		return false
	}

	if len(audioData) == 0 {
		fmt.Println("  FAIL: no audio captured")
		return false
	}

	fmt.Printf("  Recorded %.1f KB, transcribing...\n", float64(len(audioData))/1024)

	// Transcribe
	result, err := trans.Transcribe(audioData, "flac")
	if err != nil {
		fmt.Printf("  FAIL: transcription error: %v\n", err)
		return false
	}

	text := strings.TrimSpace(result.Text)
	if text == "" {
		text = "(no speech detected)"
	}

	fmt.Printf("\n  Transcribed text: %s\n\n", text)

	// Ask user to confirm - fresh reader to clear any buffered input
	confirmReader := bufio.NewReader(os.Stdin)
	fmt.Print("Is this correct? [y/n]: ")
	confirm, _ := confirmReader.ReadString('\n')
	confirm = strings.TrimSpace(strings.ToLower(confirm))

	if confirm == "y" || confirm == "yes" {
		fmt.Println("  PASS: transcription verified by user")
		return true
	}

	fmt.Println("  FAIL: transcription not confirmed")
	return false
}

func recordAudio(ctx audio.Context, device *audio.DeviceInfo, keyup <-chan struct{}) ([]byte, error) {
	enc, err := encoder.NewFlac()
	if err != nil {
		return nil, err
	}

	var sampleBuf []int16
	var bufMu sync.Mutex
	var stopped bool
	done := make(chan struct{})

	config := audio.CaptureConfig{
		SampleRate: encoder.SampleRate,
		Channels:   encoder.Channels,
	}

	captureDevice, err := ctx.NewCapture(device, config)
	if err != nil {
		return nil, err
	}

	captureDevice.SetCallback(func(data []byte, frameCount uint32) {
		bufMu.Lock()
		if stopped {
			bufMu.Unlock()
			return
		}

		for i := 0; i < len(data); i += 2 {
			sample := int16(binary.LittleEndian.Uint16(data[i:]))
			sampleBuf = append(sampleBuf, sample)
		}

		var blocks [][]int16
		for len(sampleBuf) >= encoder.BlockSize {
			block := make([]int16, encoder.BlockSize)
			copy(block, sampleBuf[:encoder.BlockSize])
			sampleBuf = sampleBuf[encoder.BlockSize:]
			blocks = append(blocks, block)
		}
		bufMu.Unlock()

		for _, block := range blocks {
			enc.EncodeBlock(block)
		}
	})

	if err := captureDevice.Start(); err != nil {
		captureDevice.Close()
		return nil, err
	}

	fmt.Print("  Recording")
	ticker := time.NewTicker(500 * time.Millisecond)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Print(".")
			}
		}
	}()

	// Wait for keyup
	<-keyup
	close(done)

	captureDevice.Stop()
	fmt.Println(" done")
	captureDevice.Close()

	// Encode remaining samples
	bufMu.Lock()
	stopped = true
	if len(sampleBuf) > 0 {
		enc.EncodeBlock(sampleBuf)
	}
	bufMu.Unlock()

	if err := enc.Close(); err != nil {
		return nil, err
	}

	return enc.Bytes(), nil
}

func checkClipboard() bool {
	fmt.Println()
	fmt.Println("[3/3] Clipboard and paste")

	if err := clipboard.Init(); err != nil {
		fmt.Printf("  Warning: paste init: %v\n", err)
	}

	fmt.Println("Focus on a text editor window...")
	for i := 5; i > 0; i-- {
		fmt.Printf("  %d...\n", i)
		time.Sleep(1 * time.Second)
	}

	testStr := "zee-doctor-test"
	if err := clipboard.Copy(testStr); err != nil {
		fmt.Printf("  FAIL: clipboard copy failed: %v\n", err)
		return false
	}

	if err := clipboard.Paste(); err != nil {
		fmt.Printf("  FAIL: paste failed: %v\n", err)
		return false
	}

	// Reset terminal and use fresh reader for confirmation
	resetTerminal()
	confirmReader := bufio.NewReader(os.Stdin)
	fmt.Println()
	fmt.Print("Did the text \"zee-doctor-test\" appear? [y/n]: ")
	confirm, _ := confirmReader.ReadString('\n')
	confirm = strings.TrimSpace(strings.ToLower(confirm))

	if confirm == "y" || confirm == "yes" {
		fmt.Println("  PASS: clipboard and paste verified by user")
		return true
	}

	fmt.Println("  FAIL: clipboard/paste not confirmed")
	return false
}
