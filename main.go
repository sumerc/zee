package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"


	"ses9000/audio"
	"ses9000/clipboard"
	"ses9000/doctor"
	"ses9000/encoder"
	"ses9000/shortcut"
	"ses9000/transcriber"
)

var version = "dev"

var peakAlloc uint64
var activeTranscriber transcriber.Transcriber
var autoPaste bool
var saveLastRecording bool
var transcriptions []TranscriptionRecord
var percentileStats PercentileStats

type PercentileStats struct {
	TotalMs  [5]float64 // min, p50, p90, p95, max
	EncodeMs [5]float64
	TLSMs    [5]float64
	CompPct  [5]float64
}

type TranscriptionRecord struct {
	AudioLengthS     float64
	RawSizeKB        float64
	CompressedSizeKB float64
	CompressionPct   float64
	EncodeTimeMs     float64
	DNSTimeMs        float64
	TLSTimeMs        float64
	NetInferMs       float64
	TotalTimeMs      float64
	MemoryAllocMB    float64
	MemoryPeakMB     float64
}

type modeConfig struct {
	name    string
	format  string // "mp3" or "flac"
	bitrate int    // MP3 bitrate in kbps (ignored for flac)
}

var modes = map[string]modeConfig{
	"fast":     {name: "fast", format: "mp3", bitrate: 16},
	"balanced": {name: "balanced", format: "mp3", bitrate: 64},
	"precise":  {name: "precise", format: "flac", bitrate: 0},
}

var activeMode modeConfig


func run() {
	benchmarkFile := flag.String("benchmark", "", "Run benchmark with WAV file instead of live recording")
	benchmarkRuns := flag.Int("runs", 3, "Number of benchmark iterations")
	autoPasteFlag := flag.Bool("autopaste", true, "Auto-paste to focused window after transcription")
	setupFlag := flag.Bool("setup", false, "Select microphone device (otherwise uses system default)")
	modeFlag := flag.String("mode", "fast", "Transcription mode: fast, balanced, or precise")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	saveRecording := flag.Bool("saverecording", false, "Save last recording to /tmp/ses9000_last.<format>")
	doctorFlag := flag.Bool("doctor", false, "Run system diagnostics and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("ses9000 %s\n", version)
		os.Exit(0)
	}

	if *doctorFlag {
		wavFile := ""
		if len(flag.Args()) > 0 {
			wavFile = flag.Args()[0]
		}
		os.Exit(doctor.Run(wavFile))
	}
	autoPaste = *autoPasteFlag
	saveLastRecording = *saveRecording

	m, ok := modes[*modeFlag]
	if !ok {
		fmt.Printf("Error: unknown mode %q (use fast, balanced, or precise)\n", *modeFlag)
		os.Exit(1)
	}
	activeMode = m
	activeTranscriber = transcriber.New()

	if err := initLogging(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not init logging: %v\n", err)
	} else {
		logSessionStart(activeTranscriber.Name(), activeMode.name, activeMode.format)
	}

	if *benchmarkFile != "" {
		runBenchmark(*benchmarkFile, *benchmarkRuns)
		return
	}

	// Init virtual keyboard early so compositor recognizes it before first paste
	if autoPaste {
		if err := clipboard.Init(); err != nil {
			fmt.Printf("Warning: paste init failed: %v\n", err)
			fmt.Println("Fix with: sudo chmod 660 /dev/uinput && sudo chgrp input /dev/uinput")
		}
	}

	// Init audio context and device selection BEFORE TUI (needs raw terminal)
	ctx, err := audio.NewContext()
	if err != nil {
		logDiagError(fmt.Sprintf("audio context init error: %v", err))
		fmt.Printf("Error initializing audio context: %v\n", err)
		os.Exit(1)
	}
	defer ctx.Close()

	var selectedDevice *audio.DeviceInfo
	if *setupFlag {
		selectedDevice, err = selectDevice(ctx)
		if err != nil {
			logDiagWarn(fmt.Sprintf("device selection failed: %v", err))
			fmt.Printf("Warning: device selection failed: %v\n", err)
			fmt.Println("Falling back to default device")
			selectedDevice = nil
		}
	}

	// Start TUI
	tuiMu.Lock()
	tuiProgram = NewTUIProgram()
	tuiMu.Unlock()

	go func() {
		if _, err := tuiProgram.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}()

	// Wait for TUI to initialize
	time.Sleep(100 * time.Millisecond)

	// Write metrics on exit
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sigChan
		if len(transcriptions) > 0 {
			logSessionEnd(len(transcriptions))
		}
		closeLogging()
		tuiProgram.Quit()
		os.Exit(0)
	}()

	// Continuous connection warming - resets after each transcription
	warmReset := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				activeTranscriber.WarmConnection()
			case <-warmReset:
				ticker.Reset(10 * time.Second)
			}
		}
	}()
	go activeTranscriber.WarmConnection() // Initial warm
	go func() { soundOnce.Do(initSound) }()

	hk := shortcut.New()
	if err := hk.Register(); err != nil {
		logDiagError(fmt.Sprintf("hotkey register error: %v", err))
		fmt.Printf("Error registering hotkey: %v\n", err)
		os.Exit(1)
	}
	defer hk.Unregister()

	formatLabel := "FLAC"
	if activeMode.format == "mp3" {
		formatLabel = fmt.Sprintf("MP3@%dkbps", activeMode.bitrate)
	}
	tuiProgram.Send(ModeLineMsg{Text: fmt.Sprintf("[%s | %s | %s]", activeMode.name, formatLabel, activeTranscriber.Name())})
	if selectedDevice != nil {
		tuiProgram.Send(DeviceLineMsg{Text: "mic: " + selectedDevice.Name})
	} else {
		tuiProgram.Send(DeviceLineMsg{Text: "mic: system default"})
	}

	for {
		<-hk.Keydown()
		tuiProgram.Send(RecordingStartMsg{})
		go activeTranscriber.WarmConnection() // Ensure fresh connection before recording

		state, err := recordWithStreaming(ctx, hk.Keyup(), selectedDevice)
		tuiProgram.Send(RecordingStopMsg{})
		if err != nil {
			logToTUI("Error recording: %v", err)
			logDiagError(fmt.Sprintf("recording error: %v", err))
			continue
		}

		if state.TotalFrames() < uint64(encoder.SampleRate/10) {
			continue
		}

		processRecording(state)
		// Reset warm timer - transcription just used the connection
		select {
		case warmReset <- struct{}{}:
		default:
		}
	}
}

func recordWithStreaming(ctx audio.Context, keyup <-chan struct{}, device *audio.DeviceInfo) (encoder.Encoder, error) {
	var enc encoder.Encoder
	var err error
	if activeMode.format == "mp3" {
		enc, err = encoder.NewMp3(activeMode.bitrate)
	} else {
		enc, err = encoder.NewFlac()
	}
	if err != nil {
		return nil, err
	}

	blockChan := make(chan []int16, 64)
	encodeDone := make(chan struct{})

	go func() {
		defer close(encodeDone)
		for block := range blockChan {
			start := time.Now()
			if err := enc.EncodeBlock(block); err != nil {
				logToTUI("Encode error: %v", err)
				logDiagError(fmt.Sprintf("encode error: %v", err))
			}
			enc.AddEncodeTime(time.Since(start))
		}
	}()

	// Audio capture state
	var sampleBuf []int16
	var bufMu sync.Mutex
	var stopped bool
	done := make(chan struct{})

	config := audio.CaptureConfig{
		SampleRate: encoder.SampleRate,
		Channels:   encoder.Channels,
	}

	captureDevice, err := ctx.NewCapture(device, config, func(data []byte, frameCount uint32) {
		bufMu.Lock()
		if stopped {
			bufMu.Unlock()
			return
		}

		// Convert bytes to int16 and calculate RMS for audio level
		var sumSquares float64
		for i := 0; i < len(data); i += 2 {
			sample := int16(binary.LittleEndian.Uint16(data[i:]))
			sampleBuf = append(sampleBuf, sample)
			normalized := float64(sample) / 32768.0
			sumSquares += normalized * normalized
		}

		// Collect full blocks while holding lock
		var blocks [][]int16
		for len(sampleBuf) >= encoder.BlockSize {
			block := make([]int16, encoder.BlockSize)
			copy(block, sampleBuf[:encoder.BlockSize])
			sampleBuf = sampleBuf[encoder.BlockSize:]
			blocks = append(blocks, block)
		}
		bufMu.Unlock()

		// Send audio level to TUI (outside lock)
		if len(data) > 0 {
			rms := math.Sqrt(sumSquares / float64(len(data)/2))
			tuiProgram.Send(AudioLevelMsg{Level: rms})
		}

		// Send blocks to encoder (outside lock — won't stall next callback)
		for _, block := range blocks {
			blockChan <- block
		}
	})
	if err != nil {
		close(blockChan)
		return nil, err
	}

	if err := captureDevice.Start(); err != nil {
		captureDevice.Close()
		close(blockChan)
		return nil, err
	}

	playStartSound()

	recordStart := time.Now()

	// Timer display goroutine
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				elapsed := time.Since(recordStart).Seconds()
				tuiProgram.Send(RecordingTickMsg{Duration: elapsed})
			}
		}
	}()

	// Wait for keyup
	go func() {
		<-keyup
		close(done)
	}()
	<-done

	captureDevice.Stop()
	playEndSound()
	captureDevice.Close()

	// Send remaining samples as partial block
	bufMu.Lock()
	stopped = true
	if len(sampleBuf) > 0 {
		partial := make([]int16, len(sampleBuf))
		copy(partial, sampleBuf)
		blockChan <- partial
	}
	bufMu.Unlock()

	close(blockChan)
	<-encodeDone

	if err := enc.Close(); err != nil {
		return nil, err
	}

	return enc, nil
}

func processRecording(enc encoder.Encoder) {
	audioDuration := float64(enc.TotalFrames()) / float64(encoder.SampleRate)
	audioData := enc.Bytes()

	if saveLastRecording {
		path := fmt.Sprintf("ses9000_last.%s", activeMode.format)
		if err := os.WriteFile(path, audioData, 0644); err != nil {
			logToTUI("save recording error: %v", err)
		}
	}

	result, err := activeTranscriber.Transcribe(audioData, activeMode.format)
	if err != nil {
		logToTUI("Error transcribing: %v", err)
		logDiagError(fmt.Sprintf("transcribe error: %v", err))
		return
	}

	text := strings.TrimSpace(result.Text)
	metrics := result.Metrics

	noSpeech := text == ""

	// Only copy/paste if there's actual speech
	clipboardOK := false
	if !noSpeech {
		clipboardOK = true
		if err := clipboard.Copy(text); err != nil {
			clipboardOK = false
			logDiagError(fmt.Sprintf("clipboard error: %v", err))
		}
		if autoPaste && clipboardOK {
			if err := clipboard.Paste(); err != nil {
				logDiagError(fmt.Sprintf("autopaste error: %v", err))
			}
		}
	}

	connStatus := "new"
	if metrics.ConnReused {
		connStatus = "reused"
	}

	rawSize := enc.TotalFrames() * 2
	encodedSize := uint64(len(audioData))
	compressionPct := (1.0 - float64(encodedSize)/float64(rawSize)) * 100

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	if m.Alloc > peakAlloc {
		peakAlloc = m.Alloc
	}

	networkAI := metrics.Upload + metrics.Inference
	total := metrics.DNS + metrics.TLS + networkAI

	formatLabel := "FLAC"
	if activeMode.format == "mp3" {
		formatLabel = fmt.Sprintf("MP3@%dkbps", activeMode.bitrate)
	}
	// Build metrics lines for TUI
	metricsLines := []string{
		fmt.Sprintf("audio:      %.1fs | %.1f KB → %.1f KB (%.0f%% smaller)",
			audioDuration, float64(rawSize)/1024, float64(encodedSize)/1024, compressionPct),
		fmt.Sprintf("mode:       %s (%s)", activeMode.name, formatLabel),
		fmt.Sprintf("encode:     %dms (concurrent)", enc.EncodeTime().Milliseconds()),
		fmt.Sprintf("dns:        %dms", metrics.DNS.Milliseconds()),
		fmt.Sprintf("tls:        %dms (%s)", metrics.TLS.Milliseconds(), connStatus),
		fmt.Sprintf("net+infer:  %dms", networkAI.Milliseconds()),
		fmt.Sprintf("total:      %dms", total.Milliseconds()),
	}
	// API confidence fields
	if result.Duration > 0 {
		metricsLines = append(metricsLines, fmt.Sprintf("api_dur:    %.2fs", result.Duration))
	}
	if result.Confidence > 0 {
		metricsLines = append(metricsLines, fmt.Sprintf("confidence: %.4f", result.Confidence))
	}
	for i, seg := range result.Segments {
		metricsLines = append(metricsLines,
			fmt.Sprintf("seg[%d]:     no_speech=%.3f logprob=%.2f comp=%.2f temp=%.1f",
				i, seg.NoSpeechProb, seg.AvgLogProb, seg.CompressionRatio, seg.Temperature))
	}

	displayText := text
	if noSpeech {
		displayText = "(no speech detected)"
	}
	tuiProgram.Send(TranscriptionMsg{
		Text:     displayText,
		Metrics:  metricsLines,
		Copied:   clipboardOK,
		NoSpeech: noSpeech,
	})

	if result.RateLimit != "?/?" {
		tuiProgram.Send(RateLimitMsg{Text: "requests: " + result.RateLimit + " remaining"})
	}

	record := TranscriptionRecord{
		AudioLengthS:     audioDuration,
		RawSizeKB:        float64(rawSize) / 1024,
		CompressedSizeKB: float64(encodedSize) / 1024,
		CompressionPct:   compressionPct,
		EncodeTimeMs:     float64(enc.EncodeTime().Milliseconds()),
		DNSTimeMs:        float64(metrics.DNS.Milliseconds()),
		TLSTimeMs:        float64(metrics.TLS.Milliseconds()),
		NetInferMs:       float64(networkAI.Milliseconds()),
		TotalTimeMs:      float64(total.Milliseconds()),
		MemoryAllocMB:    float64(m.Alloc) / 1024 / 1024,
		MemoryPeakMB:     float64(peakAlloc) / 1024 / 1024,
	}
	transcriptions = append(transcriptions, record)
	updatePercentileStats()
	logTranscriptionMetrics(record, activeMode.name, activeMode.format, activeTranscriber.Name(), metrics.ConnReused)
	logSegments(result.Segments, result.Confidence)
	if !noSpeech {
		logTranscriptionText(text)
	}
}

func updatePercentileStats() {
	n := len(transcriptions)
	if n == 0 {
		return
	}

	extract := func(fn func(TranscriptionRecord) float64) []float64 {
		vals := make([]float64, n)
		for i, r := range transcriptions {
			vals[i] = fn(r)
		}
		sort.Float64s(vals)
		return vals
	}

	percentile := func(sorted []float64, p float64) float64 {
		idx := int(float64(len(sorted)-1) * p)
		return sorted[idx]
	}

	calcStats := func(sorted []float64) [5]float64 {
		return [5]float64{
			sorted[0],
			percentile(sorted, 0.50),
			percentile(sorted, 0.90),
			percentile(sorted, 0.95),
			sorted[len(sorted)-1],
		}
	}

	percentileStats.TotalMs = calcStats(extract(func(r TranscriptionRecord) float64 { return r.TotalTimeMs }))
	percentileStats.EncodeMs = calcStats(extract(func(r TranscriptionRecord) float64 { return r.EncodeTimeMs }))
	percentileStats.TLSMs = calcStats(extract(func(r TranscriptionRecord) float64 { return r.TLSTimeMs }))
	percentileStats.CompPct = calcStats(extract(func(r TranscriptionRecord) float64 { return r.CompressionPct }))
}

func runBenchmark(wavFile string, runs int) {
	fmt.Printf("Benchmark: %s (%d runs)\n", wavFile, runs)
	fmt.Println("Using same streaming encoder as live recording")

	activeTranscriber.WarmConnection()

	for i := 1; i <= runs; i++ {
		fmt.Printf("=== Run %d ===\n", i)

		state, err := simulateRecording(wavFile)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}

		processRecording(state)
		fmt.Println()

		if i < runs {
			time.Sleep(500 * time.Millisecond)
		}
	}
}

func simulateRecording(wavFile string) (encoder.Encoder, error) {
	data, err := os.ReadFile(wavFile)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	if len(data) < 44 {
		return nil, fmt.Errorf("invalid WAV file")
	}

	audioData := data[44:]
	samples := make([]int16, len(audioData)/2)
	for i := 0; i < len(samples); i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(audioData[i*2:]))
	}

	audioDuration := float64(len(samples)) / float64(encoder.SampleRate)
	fmt.Printf("Simulating %.1fs recording...\n", audioDuration)

	var enc encoder.Encoder
	if activeMode.format == "mp3" {
		enc, err = encoder.NewMp3(activeMode.bitrate)
	} else {
		enc, err = encoder.NewFlac()
	}
	if err != nil {
		return nil, err
	}

	for i := 0; i < len(samples); i += encoder.BlockSize {
		end := i + encoder.BlockSize
		if end > len(samples) {
			end = len(samples)
		}
		block := samples[i:end]

		start := time.Now()
		if err := enc.EncodeBlock(block); err != nil {
			return nil, fmt.Errorf("encoding block: %w", err)
		}
		enc.AddEncodeTime(time.Since(start))
	}

	if err := enc.Close(); err != nil {
		return nil, err
	}

	return enc, nil
}

