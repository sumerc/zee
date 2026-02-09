package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"
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
	"zee/update"
)

var version = "dev"

const voiceThreshold = 0.002

var activeTranscriber transcriber.Transcriber
var autoPaste bool
var transcriptionsMu sync.Mutex
var transcriptions []TranscriptionRecord
var percentileStats PercentileStats
var streamEnabled bool
var activeFormat string

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

var deviceSelectChan = make(chan struct{}, 1)

func deviceLineText(dev *audio.DeviceInfo) string {
	name := "system default"
	if dev != nil {
		name = dev.Name
	}
	return "mic: " + name + " (ctrl+g)"
}

const recordTail = 500 * time.Millisecond

func run() {
	if len(os.Args) > 1 && os.Args[1] == "update" {
		if version == "dev" {
			fmt.Println("Dev build — cannot check for updates.")
			os.Exit(0)
		}
		fmt.Printf("zee %s — checking for updates...\n", version)
		rel, err := update.CheckLatest(version)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		if rel == nil {
			fmt.Println("Already up to date.")
			os.Exit(0)
		}
		fmt.Printf("Update available: %s -> %s\n", version, rel.Version)
		fmt.Print("Continue? [y/N] ")
		var answer string
		fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" {
			fmt.Println("Aborted.")
			os.Exit(0)
		}
		fmt.Printf("Downloading %s...\n", rel.Version)
		if err := update.Apply(rel); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Updated to %s\n", rel.Version)
		os.Exit(0)
	}

	benchmarkFile := flag.String("benchmark", "", "Run benchmark with WAV file instead of live recording")
	benchmarkRuns := flag.Int("runs", 3, "Number of benchmark iterations")
	autoPasteFlag := flag.Bool("autopaste", true, "Auto-paste to focused window after transcription")
	streamFlag := flag.Bool("stream", false, "Enable streaming transcription (Deepgram only)")
	setupFlag := flag.Bool("setup", false, "Select microphone device (otherwise uses system default)")
	formatFlag := flag.String("format", "mp3@16", "Audio format: mp3@16, mp3@64, or flac")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	doctorFlag := flag.Bool("doctor", false, "Run system diagnostics and exit")
	expertFlag := flag.Bool("expert", false, "Show full TUI with HAL eye animation")
	langFlag := flag.String("lang", "", "Language code for transcription (e.g., en, es, fr). Empty = auto-detect")
	crashFlag := flag.Bool("crash", false, "Trigger synthetic panic for testing crash logging")
	logPathFlag := flag.String("logpath", "", "log directory path (default: OS-specific location, use ./ for current dir)")
	profileFlag := flag.String("profile", "", "Enable pprof profiling server (e.g., :6060 or localhost:6060)")
	testFlag := flag.Bool("test", false, "Test mode (headless, stdin-driven)")
	hybridFlag := flag.Bool("hybrid", false, "Enable hybrid tap+hold recording mode")
	longPressFlag := flag.Duration("longpress", 350*time.Millisecond, "Long-press threshold for PTT vs tap (e.g., 350ms)")
	flag.Parse()

	// Resolve log directory early
	logPath, err := log.ResolveDir(*logPathFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to resolve log directory: %v\n", err)
		os.Exit(1)
	}
	log.SetDir(logPath)

	if err := log.EnsureDir(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create log directory: %v\n", err)
	}

	crashPath := filepath.Join(log.Dir(), "crash_log.txt")
	crashFile, err := os.OpenFile(crashPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		fmt.Fprintf(crashFile, "\n=== Session %s [pid=%d] ===\n", time.Now().Format("2006-01-02 15:04:05"), os.Getpid())
		debug.SetCrashOutput(crashFile, debug.CrashOptions{})
	}

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
	streamEnabled = *streamFlag

	// Validate format
	switch *formatFlag {
	case "mp3@16", "mp3@64", "flac":
		activeFormat = *formatFlag
	default:
		fmt.Printf("Error: unknown format %q (use mp3@16, mp3@64, or flac)\n", *formatFlag)
		os.Exit(1)
	}

	if streamEnabled && *formatFlag != "mp3@16" {
		log.Warn("format ignored in streaming mode")
	}

	var initErr error
	activeTranscriber, initErr = transcriber.New()
	if initErr != nil {
		fmt.Printf("Error: %v\n", initErr)
		os.Exit(1)
	}
	if *langFlag != "" {
		activeTranscriber.SetLanguage(*langFlag)
	}

	// Only enable verbose logging in expert mode
	if *expertFlag {
		if err := log.Init(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not init logging: %v\n", err)
		} else {
			log.SessionStart(activeTranscriber.Name(), activeFormat, activeFormat)
		}
	}

	if *testFlag {
		args := flag.Args()
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "Usage: zee -test <wav-file>")
			os.Exit(1)
		}
		runTestMode(args[0])
		return
	}

	if *benchmarkFile != "" {
		runBenchmark(*benchmarkFile, *benchmarkRuns)
		return
	}

	if autoPaste {
		if err := clipboard.Init(); err != nil {
			fmt.Printf("Warning: paste init failed: %v\n", err)
			fmt.Println("Fix with: sudo chmod 660 /dev/uinput && sudo chgrp input /dev/uinput")
		}
	}

	ctx, err := audio.NewContext()
	if err != nil {
		log.Errorf("audio context init error: %v", err)
		fmt.Printf("Error initializing audio context: %v\n", err)
		os.Exit(1)
	}
	defer ctx.Close()

	var selectedDevice *audio.DeviceInfo
	if *setupFlag {
		selectedDevice, err = selectDevice(ctx)
		if err != nil {
			log.Warnf("device selection failed: %v", err)
			fmt.Printf("Warning: device selection failed: %v\n", err)
			fmt.Println("Falling back to default device")
			selectedDevice = nil
		}
	}

	captureConfig := audio.CaptureConfig{
		SampleRate: encoder.SampleRate,
		Channels:   encoder.Channels,
	}
	captureDevice, err := ctx.NewCapture(selectedDevice, captureConfig)
	if err != nil {
		log.Errorf("capture device init error: %v", err)
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
			log.Errorf("TUI error: %v", err)
			os.Exit(1)
		}
		os.Exit(0)
	}()

	<-tuiReady

	update.StartBackgroundCheck(version, log.Dir(), func(rel update.Release) {
		tuiSend(UpdateAvailableMsg{Version: rel.Version})
	})

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

	go beep.Init()

	hk := hotkey.New()
	if err := hk.Register(); err != nil {
		log.Errorf("hotkey register error: %v", err)
		fmt.Printf("Error registering hotkey: %v\n", err)
		os.Exit(1)
	}
	defer hk.Unregister()

	providerLabel := activeTranscriber.Name()
	if lang := activeTranscriber.GetLanguage(); lang != "" {
		providerLabel += " (" + lang + ")"
	}
	formatLabel := activeFormat
	if streamEnabled {
		providerLabel += " (stream)"
		formatLabel = "PCM16"
	}
	tuiSend(ModeLineMsg{Text: fmt.Sprintf("[%s | %s]", formatLabel, providerLabel)})
	tuiSend(DeviceLineMsg{Text: deviceLineText(selectedDevice)})
	tuiSend(HybridHelpMsg{Enabled: *hybridFlag})

	if *hybridFlag {
		hy := hotkey.NewHybrid(hk, *longPressFlag)
		for {
			select {
			case ev := <-hy.Start():
				log.Info("hotkey_start_" + string(ev.Mode))
				tuiSend(RecordingStartMsg{})
				go beep.PlayStart()

				_, err := handleRecording(captureDevice, hy.StopChan())
				if err != nil {
					logToTUI("Error recording: %v", err)
					log.Errorf("recording error: %v", err)
				}

			case <-deviceSelectChan:
				handleDeviceSwitch(ctx, captureConfig, &captureDevice, &selectedDevice)
			}
		}
	} else {
		for {
			select {
			case <-hk.Keydown():
				log.Info("hotkey_down")
				tuiSend(RecordingStartMsg{})
				go beep.PlayStart()

				_, err := handleRecording(captureDevice, hk.Keyup())
				if err != nil {
					logToTUI("Error recording: %v", err)
					log.Errorf("recording error: %v", err)
				}

			case <-deviceSelectChan:
				handleDeviceSwitch(ctx, captureConfig, &captureDevice, &selectedDevice)
			}
		}
	}
}

func handleDeviceSwitch(ctx audio.Context, captureConfig audio.CaptureConfig, captureDevice *audio.CaptureDevice, selectedDevice **audio.DeviceInfo) {
	tuiProgram.ReleaseTerminal()
	newDevice, err := selectDevice(ctx)
	tuiProgram.RestoreTerminal()

	if err != nil {
		log.Warnf("device selection failed: %v", err)
		return
	}
	if newDevice != nil {
		(*captureDevice).Close()
		newCapture, err := ctx.NewCapture(newDevice, captureConfig)
		if err != nil {
			log.Errorf("capture device reinit error: %v", err)
			return
		}
		*captureDevice = newCapture
		*selectedDevice = newDevice
		tuiSend(DeviceLineMsg{Text: deviceLineText(newDevice)})
	}
}

func handleRecording(capture audio.CaptureDevice, keyup <-chan struct{}) (<-chan struct{}, error) {
	sess, err := activeTranscriber.NewSession(context.Background(), transcriber.SessionConfig{
		Stream:   streamEnabled,
		Format:   activeFormat,
		Language: activeTranscriber.GetLanguage(),
	})
	if err != nil {
		return nil, err
	}

	clipCh := make(chan string, 1)
	if autoPaste {
		go func() {
			prev, _ := clipboard.Read()
			clipCh <- prev
		}()
	}

	var lastTranscript atomic.Int64
	updatesDone := make(chan struct{})
	go func() {
		defer close(updatesDone)
		var prev string
		for text := range sess.Updates() {
			lastTranscript.Store(time.Now().UnixNano())
			tuiSend(LiveTranscriptionMsg{Text: text})
			if autoPaste {
				delta := text[len(prev):]
				if delta != "" {
					clipboard.Copy(delta)
					clipboard.Paste()
				}
			}
			prev = text
		}
	}()

	totalFrames, err := record(capture, keyup, sess, &lastTranscript)

	if err != nil {
		sess.Close()
		return nil, err
	}
	if totalFrames < uint64(encoder.SampleRate/10) {
		sess.Close()
		return nil, nil
	}

	done := make(chan struct{})
	go func() {
		finishTranscription(sess, clipCh, updatesDone)
		close(done)
	}()
	return done, nil
}

func finishTranscription(sess transcriber.Session, clipCh chan string, updatesDone <-chan struct{}) {
	result, closeErr := sess.Close()
	<-updatesDone // wait for updates goroutine to drain

	var clipPrev string
	if autoPaste {
		clipPrev = <-clipCh
	}
	tuiSend(LiveTranscriptionMsg{Text: ""})

	if closeErr != nil {
		log.Errorf("transcription error: %v", closeErr)
		logToTUI("Error: %v", closeErr)
	}

	if closeErr == nil && !streamEnabled && result.HasText && autoPaste {
		clipboard.Copy(result.Text)
		clipboard.Paste()
	}

	if autoPaste && clipPrev != "" {
		go func() {
			time.Sleep(600 * time.Millisecond)
			clipboard.Copy(clipPrev)
		}()
	}

	if closeErr != nil {
		return
	}

	displayText := result.Text
	if result.NoSpeech {
		displayText = "(no speech detected)"
	}
	tuiSend(TranscriptionMsg{Text: displayText, Metrics: result.Metrics, NoSpeech: result.NoSpeech})

	if result.RateLimit != "" && result.RateLimit != "?/?" {
		tuiSend(RateLimitMsg{Text: "requests: " + result.RateLimit + " remaining"})
	}

	if result.Batch != nil {
		bs := result.Batch
		record := TranscriptionRecord{
			AudioLengthS:     bs.AudioLengthS,
			RawSizeKB:        bs.RawSizeKB,
			CompressedSizeKB: bs.CompressedSizeKB,
			CompressionPct:   bs.CompressionPct,
			EncodeTimeMs:     bs.EncodeTimeMs,
			DNSTimeMs:        bs.DNSTimeMs,
			TLSTimeMs:        bs.TLSTimeMs,
			TTFBMs:           bs.TTFBMs,
			TotalTimeMs:      bs.TotalTimeMs,
			MemoryAllocMB:    result.MemoryAllocMB,
			MemoryPeakMB:     result.MemoryPeakMB,
		}
		transcriptionsMu.Lock()
		transcriptions = append(transcriptions, record)
		updatePercentileStats()
		transcriptionsMu.Unlock()
		log.TranscriptionMetrics(log.Metrics(record), activeFormat, activeFormat, activeTranscriber.Name(), bs.ConnReused, bs.TLSProtocol)
		log.Confidence(bs.Confidence)
	}

	if result.Stream != nil {
		ss := result.Stream
		log.StreamMetrics(log.StreamMetricsData{
			ConnectMs:    ss.ConnectMs,
			FinalizeMs:   ss.FinalizeMs,
			TotalMs:      ss.TotalMs,
			AudioS:       ss.AudioS,
			SentChunks:   ss.SentChunks,
			SentKB:       ss.SentKB,
			RecvMessages: ss.RecvMessages,
			RecvFinal:    ss.RecvFinal,
			CommitEvents: ss.CommitEvents,
		})
	}

	if !result.NoSpeech {
		log.TranscriptionText(result.Text)
	}
}

func record(capture audio.CaptureDevice, keyup <-chan struct{}, sess transcriber.Session, lastTranscript *atomic.Int64) (uint64, error) {
	var bufMu sync.Mutex
	var totalFrames uint64
	var peakLevel float64
	var noVoiceBeeped bool
	var stopped bool
	var voiceDetected bool
	var lastVoiceTime time.Time
	done := make(chan struct{})

	capture.SetCallback(func(data []byte, frameCount uint32) {
		bufMu.Lock()
		if stopped {
			bufMu.Unlock()
			return
		}
		totalFrames += uint64(frameCount)
		bufMu.Unlock()

		if len(data) > 0 {
			pcm := make([]byte, len(data))
			copy(pcm, data)
			sess.Feed(pcm)
		}

		if len(data) > 1 {
			var sumSquares float64
			for i := 0; i+1 < len(data); i += 2 {
				sample := int16(binary.LittleEndian.Uint16(data[i:]))
				normalized := float64(sample) / 32768.0
				sumSquares += normalized * normalized
			}
			rms := math.Sqrt(sumSquares / float64(len(data)/2))
			tuiSend(AudioLevelMsg{Level: rms})
			bufMu.Lock()
			if rms > peakLevel {
				peakLevel = rms
			}
			if rms >= voiceThreshold {
				if !voiceDetected {
					voiceDetected = true
				}
				lastVoiceTime = time.Now()
			}
			bufMu.Unlock()
		}
	})

	if err := capture.Start(); err != nil {
		capture.ClearCallback()
		return 0, err
	}

	recordStart := time.Now()
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
				checkNoVoice(&bufMu, elapsed, &peakLevel, &noVoiceBeeped)
				bufMu.Lock()
				vd := voiceDetected
				bufMu.Unlock()
				if vd {
					checkSilenceDuring(&bufMu, &lastVoiceTime)
				}
				if streamEnabled {
					checkTranscriptSilence(lastTranscript)
				}
			}
		}
	}()

	go func() {
		<-keyup
		log.Info("hotkey_up")
		tuiSend(RecordingStopMsg{})
		go beep.PlayEnd()
		if streamEnabled {
			time.Sleep(recordTail)
		}
		close(done)
	}()
	<-done

	capture.Stop()
	capture.ClearCallback()

	bufMu.Lock()
	stopped = true
	frames := totalFrames
	bufMu.Unlock()

	return frames, nil
}

func checkNoVoice(mu *sync.Mutex, elapsed float64, peakLevel *float64, beeped *bool) {
	mu.Lock()
	shouldWarn := elapsed > 1.0 && *peakLevel < voiceThreshold && !*beeped
	if shouldWarn {
		*beeped = true
	}
	mu.Unlock()
	if shouldWarn {
		log.Info("no_voice_warning")
		tuiSend(NoVoiceWarningMsg{})
		beep.PlayError()
	}
}

const silenceTimeout = 8 * time.Second

func checkSilenceDuring(mu *sync.Mutex, lastVoiceTime *time.Time) {
	mu.Lock()
	shouldWarn := time.Since(*lastVoiceTime) > silenceTimeout
	if shouldWarn {
		*lastVoiceTime = time.Now()
	}
	mu.Unlock()
	if shouldWarn {
		log.Info("silence_during_warning")
		tuiSend(NoVoiceWarningMsg{})
		beep.PlayError()
	}
}

func checkTranscriptSilence(lastTranscript *atomic.Int64) {
	ts := lastTranscript.Load()
	if ts == 0 {
		return // no transcript received yet
	}
	if time.Since(time.Unix(0, ts)) > silenceTimeout {
		lastTranscript.Store(time.Now().UnixNano())
		log.Info("transcript_silence_warning")
		tuiSend(TranscriptSilenceMsg{})
		beep.PlayError()
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

	for i := 1; i <= runs; i++ {
		fmt.Printf("=== Run %d ===\n", i)

		sess, err := activeTranscriber.NewSession(context.Background(), transcriber.SessionConfig{
			Format:   activeFormat,
			Language: activeTranscriber.GetLanguage(),
		})
		if err != nil {
			fmt.Printf("Error creating session: %v\n", err)
			return
		}

		data, err := os.ReadFile(wavFile)
		if err != nil {
			fmt.Printf("Error reading file: %v\n", err)
			return
		}
		if len(data) < audio.WAVHeaderSize {
			fmt.Println("Error: invalid WAV file")
			return
		}

		audioData := data[audio.WAVHeaderSize:]
		audioDuration := float64(len(audioData)/2) / float64(encoder.SampleRate)
		fmt.Printf("Simulating %.1fs recording...\n", audioDuration)

		sess.Feed(audioData)
		result, err := sess.Close()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}

		displayText := result.Text
		if result.NoSpeech {
			displayText = "(no speech detected)"
		}
		fmt.Printf("Text: %s\n", displayText)
		for _, line := range result.Metrics {
			fmt.Printf("  %s\n", line)
		}
		fmt.Println()

		if i < runs {
			time.Sleep(500 * time.Millisecond)
		}
	}
}
