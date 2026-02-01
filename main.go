package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"zee/audio"
	"zee/beep"
	"zee/clipboard"
	"zee/doctor"
	"zee/encoder"
	"zee/hotkey"
	"zee/log"
	"zee/shutdown"
	"zee/transcriber"
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
	TTFBMs           float64
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
	"adaptive": {name: "adaptive", format: "adaptive", bitrate: 0},
}

var adaptiveThreshold = 100 * 1024 // bytes, default 100KB (fast connection), updated from TLS warmup

func thresholdFromTLSWarmup(tlsTime time.Duration) int {
	switch {
	case tlsTime < 100*time.Millisecond:
		return 100 * 1024 // 100KB for fast connections
	case tlsTime < 300*time.Millisecond:
		return 60 * 1024 // 60KB for medium
	default:
		return 30 * 1024 // 30KB for slow
	}
}

func newEncoderForMode(mode modeConfig) (encoder.Encoder, error) {
	switch mode.format {
	case "mp3":
		return encoder.NewMp3(mode.bitrate)
	case "adaptive":
		return encoder.NewAdaptive()
	default:
		return encoder.NewFlac()
	}
}

var activeMode modeConfig
var deviceSelectChan = make(chan struct{}, 1)

func deviceLineText(dev *audio.DeviceInfo) string {
	name := "system default"
	if dev != nil {
		name = dev.Name
	}
	return "mic: " + name + " (ctrl+g to change)"
}

func formatLabelForMode(mode modeConfig, adaptiveSuffix string) string {
	switch mode.format {
	case "mp3":
		return fmt.Sprintf("MP3@%dkbps", mode.bitrate)
	case "adaptive":
		return "adaptive" + adaptiveSuffix
	default:
		return "FLAC"
	}
}

func run() {
	benchmarkFile := flag.String("benchmark", "", "Run benchmark with WAV file instead of live recording")
	benchmarkRuns := flag.Int("runs", 3, "Number of benchmark iterations")
	autoPasteFlag := flag.Bool("autopaste", true, "Auto-paste to focused window after transcription")
	setupFlag := flag.Bool("setup", false, "Select microphone device (otherwise uses system default)")
	modeFlag := flag.String("mode", "fast", "Transcription mode: fast, balanced, or precise")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	saveRecording := flag.Bool("saverecording", false, "Save last recording to zee_last.<format>")
	doctorFlag := flag.Bool("doctor", false, "Run system diagnostics and exit")
	expertFlag := flag.Bool("expert", false, "Show full TUI with HAL eye animation")
	langFlag := flag.String("lang", "", "Language code for transcription (e.g., en, es, fr). Empty = auto-detect")
	crashFlag := flag.Bool("crash", false, "Trigger synthetic panic for testing crash logging")
	logPathFlag := flag.String("logpath", "", "log directory path (default: OS-specific location, use ./ for current dir)")
	profileFlag := flag.String("profile", "", "Enable pprof profiling server (e.g., :6060 or localhost:6060)")
	flag.Parse()

	// Resolve log directory early
	logPath, err := log.ResolveDir(*logPathFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to resolve log directory: %v\n", err)
		os.Exit(1)
	}
	log.SetDir(logPath)

	// Ensure log directory exists for crash log (always enabled)
	if err := log.EnsureDir(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create log directory: %v\n", err)
	}

	// Set up crash output file for all crashes (panics, SIGSEGV, etc.)
	// Write session marker so each crash can be correlated to a session start time
	crashPath := filepath.Join(log.Dir(), "crash_log.txt")
	crashFile, err := os.OpenFile(crashPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		fmt.Fprintf(crashFile, "\n=== Session %s [pid=%d] ===\n", time.Now().Format("2006-01-02 15:04:05"), os.Getpid())
		debug.SetCrashOutput(crashFile, debug.CrashOptions{})
	}

	// Start pprof server if requested
	if *profileFlag != "" {
		go func() {
			fmt.Fprintf(os.Stderr, "pprof server listening on http://%s/debug/pprof/\n", *profileFlag)
			if err := http.ListenAndServe(*profileFlag, nil); err != nil {
				fmt.Fprintf(os.Stderr, "pprof server error: %v\n", err)
			}
		}()
	}

	if *crashFlag {
		panic("TEST CRASH: synthetic panic to verify crash logging")
	}

	if *versionFlag {
		fmt.Printf("zee %s\n", version)
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
		fmt.Printf("Error: unknown mode %q (use fast, balanced, precise, or adaptive)\n", *modeFlag)
		os.Exit(1)
	}
	activeMode = m
	activeTranscriber = transcriber.New()
	if *langFlag != "" {
		activeTranscriber.SetLanguage(*langFlag)
	}

	// Only enable verbose logging in expert mode
	if *expertFlag {
		if err := log.Init(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not init logging: %v\n", err)
		} else {
			log.SessionStart(activeTranscriber.Name(), activeMode.name, activeMode.format)
		}
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
		log.Error(fmt.Sprintf("audio context init error: %v", err))
		fmt.Printf("Error initializing audio context: %v\n", err)
		os.Exit(1)
	}
	defer ctx.Close()

	var selectedDevice *audio.DeviceInfo
	if *setupFlag {
		selectedDevice, err = selectDevice(ctx)
		if err != nil {
			log.Warn(fmt.Sprintf("device selection failed: %v", err))
			fmt.Printf("Warning: device selection failed: %v\n", err)
			fmt.Println("Falling back to default device")
			selectedDevice = nil
		}
	}

	// Create persistent capture device
	captureConfig := audio.CaptureConfig{
		SampleRate: encoder.SampleRate,
		Channels:   encoder.Channels,
	}
	captureDevice, err := ctx.NewCapture(selectedDevice, captureConfig)
	if err != nil {
		log.Error(fmt.Sprintf("capture device init error: %v", err))
		fmt.Printf("Error initializing capture device: %v\n", err)
		os.Exit(1)
	}
	defer captureDevice.Close()

	// Start TUI
	tuiMu.Lock()
	tuiProgram = NewTUIProgram(*expertFlag)
	tuiMu.Unlock()

	go func() {
		if _, err := tuiProgram.Run(); err != nil {
			log.Error(fmt.Sprintf("TUI error: %v", err))
			os.Exit(1)
		}
		os.Exit(0)
	}()

	// Wait for TUI to initialize
	time.Sleep(100 * time.Millisecond)

	// Write metrics on exit
	sigChan := make(chan os.Signal, 1)
	shutdown.Notify(sigChan)
	go func() {
		<-sigChan
		if len(transcriptions) > 0 {
			log.SessionEnd(len(transcriptions))
		}
		log.Close()
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
	// Initial warmup - capture TLS time for adaptive threshold
	go func() {
		tlsTime := activeTranscriber.WarmConnection()
		if activeMode.format == "adaptive" {
			adaptiveThreshold = thresholdFromTLSWarmup(tlsTime)
		}
	}()
	go beep.Init()

	hk := hotkey.New()
	if err := hk.Register(); err != nil {
		log.Error(fmt.Sprintf("hotkey register error: %v", err))
		fmt.Printf("Error registering hotkey: %v\n", err)
		os.Exit(1)
	}
	defer hk.Unregister()

	formatLabel := formatLabelForMode(activeMode, "")
	providerLabel := activeTranscriber.Name()
	if lang := activeTranscriber.GetLanguage(); lang != "" {
		providerLabel += " (" + lang + ")"
	}
	tuiSend(ModeLineMsg{Text: fmt.Sprintf("[%s | %s | %s]", activeMode.name, formatLabel, providerLabel)})
	tuiSend(DeviceLineMsg{Text: deviceLineText(selectedDevice)})

	for {
		select {
		case <-hk.Keydown():
			log.Info("hotkey_down")
			tuiSend(RecordingStartMsg{})
			go activeTranscriber.WarmConnection() // Ensure fresh connection before recording

			state, err := recordWithStreaming(captureDevice, hk.Keyup())
			log.Info("hotkey_up")
			tuiSend(RecordingStopMsg{})
			if err != nil {
				logToTUI("Error recording: %v", err)
				log.Error(fmt.Sprintf("recording error: %v", err))
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

		case <-deviceSelectChan:
			// Pause TUI for device selection
			tuiProgram.ReleaseTerminal()

			newDevice, err := selectDevice(ctx)

			tuiProgram.RestoreTerminal()

			if err != nil {
				log.Warn(fmt.Sprintf("device selection failed: %v", err))
				continue
			}
			if newDevice != nil {
				captureDevice.Close()
				captureDevice, err = ctx.NewCapture(newDevice, captureConfig)
				if err != nil {
					log.Error(fmt.Sprintf("capture device reinit error: %v", err))
					continue
				}
				selectedDevice = newDevice
				tuiSend(DeviceLineMsg{Text: deviceLineText(newDevice)})
			}
		}
	}
}

func recordWithStreaming(capture audio.CaptureDevice, keyup <-chan struct{}) (encoder.Encoder, error) {
	enc, err := newEncoderForMode(activeMode)
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
				log.Error(fmt.Sprintf("encode error: %v", err))
			}
			enc.AddEncodeTime(time.Since(start))
		}
	}()

	// Audio capture state
	var sampleBuf []int16
	var bufMu sync.Mutex
	var stopped bool
	var peakLevel float64
	var noVoiceBeeped bool
	done := make(chan struct{})

	// Set callback for this recording
	capture.SetCallback(func(data []byte, frameCount uint32) {
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
			tuiSend(AudioLevelMsg{Level: rms})
			bufMu.Lock()
			if rms > peakLevel {
				peakLevel = rms
			}
			bufMu.Unlock()
		}

		// Send blocks to encoder (outside lock — won't stall next callback)
		for _, block := range blocks {
			blockChan <- block
		}
	})

	if err := capture.Start(); err != nil {
		capture.ClearCallback()
		close(blockChan)
		return nil, err
	}

	log.Info("beep_start")
	beep.PlayStart()

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
				tuiSend(RecordingTickMsg{Duration: elapsed})
				// Check for no voice after 1s
				bufMu.Lock()
				shouldWarn := elapsed > 1.0 && peakLevel < 0.005 && !noVoiceBeeped
				if shouldWarn {
					noVoiceBeeped = true
				}
				bufMu.Unlock()
				if shouldWarn {
					tuiSend(NoVoiceWarningMsg{})
					beep.PlayError()
				}
			}
		}
	}()

	// Wait for keyup
	go func() {
		<-keyup
		close(done)
	}()
	<-done

	capture.Stop()
	log.Info("beep_end")
	beep.PlayEnd()
	capture.ClearCallback()

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

	// Handle adaptive encoder format selection
	var audioFormat string
	var adaptiveInfo string
	if adaptiveEnc, ok := enc.(*encoder.AdaptiveEncoder); ok {
		adaptiveEnc.Select(adaptiveThreshold)
		audioFormat = adaptiveEnc.Format()
		adaptiveInfo = fmt.Sprintf(" → %s (%dKB threshold)", adaptiveEnc.ChosenName(), adaptiveThreshold/1024)
	} else {
		audioFormat = activeMode.format
	}

	audioData := enc.Bytes()

	if saveLastRecording {
		path := fmt.Sprintf("zee_last.%s", audioFormat)
		if err := os.WriteFile(path, audioData, 0644); err != nil {
			logToTUI("save recording error: %v", err)
		}
	}

	result, err := activeTranscriber.Transcribe(audioData, audioFormat)
	if err != nil {
		logToTUI("Error transcribing: %v", err)
		log.Error(fmt.Sprintf("transcribe error: %v", err))
		return
	}

	text := strings.TrimSpace(result.Text)
	metrics := result.Metrics

	noSpeech := text == ""

	// Only type if there's actual speech and autopaste is enabled
	clipboardOK := false
	if !noSpeech && autoPaste {
		log.Info("paste_start")
		if err := clipboard.Type(text); err != nil {
			log.Error(fmt.Sprintf("type error: %v", err))
		} else {
			clipboardOK = true
			log.Info("paste_done")
		}
	}

	rawSize := enc.TotalFrames() * 2
	encodedSize := uint64(len(audioData))
	compressionPct := (1.0 - float64(encodedSize)/float64(rawSize)) * 100

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	if m.Alloc > peakAlloc {
		peakAlloc = m.Alloc
	}

	total := metrics.ConnWait + metrics.DNS + metrics.TCP + metrics.TLS + metrics.ReqHeaders + metrics.ReqBody + metrics.TTFB + metrics.Download

	formatLabel := formatLabelForMode(activeMode, adaptiveInfo)
	// Build metrics lines for TUI
	reusedStatus := ""
	if metrics.ConnReused {
		reusedStatus = " (reused)"
	}
	metricsLines := []string{
		fmt.Sprintf("audio:      %.1fs | %.1f KB → %.1f KB (%.0f%% smaller)",
			audioDuration, float64(rawSize)/1024, float64(encodedSize)/1024, compressionPct),
		fmt.Sprintf("mode:       %s (%s)", activeMode.name, formatLabel),
		fmt.Sprintf("encode:     %dms (concurrent)", enc.EncodeTime().Milliseconds()),
		fmt.Sprintf("conn_wait:  %dms%s", metrics.ConnWait.Milliseconds(), reusedStatus),
		fmt.Sprintf("dns:        %dms", metrics.DNS.Milliseconds()),
		fmt.Sprintf("tcp:        %dms", metrics.TCP.Milliseconds()),
		fmt.Sprintf("tls:        %dms", metrics.TLS.Milliseconds()),
		fmt.Sprintf("req_head:   %dms", metrics.ReqHeaders.Milliseconds()),
		fmt.Sprintf("req_body:   %dms", metrics.ReqBody.Milliseconds()),
		fmt.Sprintf("ttfb:       %dms", metrics.TTFB.Milliseconds()),
		fmt.Sprintf("download:   %dms", metrics.Download.Milliseconds()),
		fmt.Sprintf("total:      %dms", total.Milliseconds()),
	}
	// API confidence fields
	if result.Duration > 0 {
		metricsLines = append(metricsLines, fmt.Sprintf("api_dur:    %.2fs", result.Duration))
	}
	if result.Confidence > 0 {
		metricsLines = append(metricsLines, fmt.Sprintf("confidence: %.4f", result.Confidence))
	}

	displayText := text
	if noSpeech {
		displayText = "(no speech detected)"
	}
	tuiSend(TranscriptionMsg{
		Text:     displayText,
		Metrics:  metricsLines,
		Copied:   clipboardOK,
		NoSpeech: noSpeech,
	})

	if result.RateLimit != "?/?" {
		tuiSend(RateLimitMsg{Text: "requests: " + result.RateLimit + " remaining"})
	}

	record := TranscriptionRecord{
		AudioLengthS:     audioDuration,
		RawSizeKB:        float64(rawSize) / 1024,
		CompressedSizeKB: float64(encodedSize) / 1024,
		CompressionPct:   compressionPct,
		EncodeTimeMs:     float64(enc.EncodeTime().Milliseconds()),
		DNSTimeMs:        float64(metrics.DNS.Milliseconds()),
		TLSTimeMs:        float64(metrics.TLS.Milliseconds()),
		TTFBMs:           float64(metrics.TTFB.Milliseconds()),
		TotalTimeMs:      float64(total.Milliseconds()),
		MemoryAllocMB:    float64(m.Alloc) / 1024 / 1024,
		MemoryPeakMB:     float64(peakAlloc) / 1024 / 1024,
	}
	transcriptions = append(transcriptions, record)
	updatePercentileStats()
	log.TranscriptionMetrics(log.Metrics(record), activeMode.name, audioFormat, activeTranscriber.Name(), metrics.ConnReused)
	log.Confidence(result.Confidence)
	if !noSpeech {
		log.TranscriptionText(text)
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

	tlsTime := activeTranscriber.WarmConnection()
	if activeMode.format == "adaptive" {
		adaptiveThreshold = thresholdFromTLSWarmup(tlsTime)
		fmt.Printf("Adaptive mode: TLS=%dms → threshold=%dKB\n", tlsTime.Milliseconds(), adaptiveThreshold/1024)
	}

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

	enc, err := newEncoderForMode(activeMode)
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
