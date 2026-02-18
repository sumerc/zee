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
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
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
	"zee/tray"
	"zee/update"
)

var version = "dev"


var activeTranscriber transcriber.Transcriber
var autoPaste bool
var transcriptionsMu sync.Mutex
var transcriptions []TranscriptionRecord
var percentileStats PercentileStats
var streamEnabled bool
var activeFormat string
var lastText string

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
var trayRecordChan = make(chan struct{}, 1)
var trayStopMu sync.Mutex
var trayStopChan chan struct{}

var shutdownOnce sync.Once

func gracefulShutdown() {
	shutdownOnce.Do(func() {
		transcriptionsMu.Lock()
		n := len(transcriptions)
		transcriptionsMu.Unlock()
		if n > 0 {
			log.SessionEnd(n)
		}
		log.Close()
		tray.Quit()
		if tuiProgram != nil {
			tuiProgram.Quit()
		}
		os.Exit(0)
	})
}

func deviceLineText(dev *audio.DeviceInfo) string {
	name := "system default"
	suffix := ""
	if dev != nil {
		name = dev.Name
		if audio.IsBluetooth(dev.Name) {
			suffix = " (BT!)"
		}
	}
	return "mic: " + name + suffix + " (ctrl+g)"
}

func modeLineText() string {
	providerLabel := activeTranscriber.Name()
	if lang := activeTranscriber.GetLanguage(); lang != "" {
		providerLabel += " (" + lang + ")"
	}
	formatLabel := activeFormat
	if streamEnabled {
		providerLabel += " (stream)"
		formatLabel = "PCM16"
	}
	return fmt.Sprintf("[%s | %s]", formatLabel, providerLabel)
}

func reportRecordingError(err error) {
	if err == nil {
		return
	}
	logToTUI("Error recording: %v", err)
	log.Errorf("recording error: %v", err)
	tray.SetError(err.Error())
}

const recordTail = 500 * time.Millisecond

func newTrayStop() <-chan struct{} {
	trayStopMu.Lock()
	trayStopChan = make(chan struct{})
	ch := trayStopChan
	trayStopMu.Unlock()
	return ch
}

func fireTrayStop() {
	trayStopMu.Lock()
	if trayStopChan != nil {
		select {
		case trayStopChan <- struct{}{}:
		default:
		}
	}
	trayStopMu.Unlock()
}

// mergeStop returns a channel that closes when any source fires.
func mergeStop(sources ...<-chan struct{}) chan struct{} {
	out := make(chan struct{})
	var once sync.Once
	for _, s := range sources {
		if s == nil {
			continue
		}
		go func(ch <-chan struct{}) {
			select {
			case <-ch:
				once.Do(func() { close(out) })
			case <-out:
			}
		}(s)
	}
	return out
}

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
	deviceFlag := flag.String("device", "", "Use named microphone device")
	formatFlag := flag.String("format", "mp3@16", "Audio format: mp3@16, mp3@64, or flac")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	doctorFlag := flag.Bool("doctor", false, "Run system diagnostics and exit")
	expertFlag := flag.Bool("expert", false, "Show full TUI with HAL eye animation")
	langFlag := flag.String("lang", "en", "Language code for transcription (e.g., en, es, fr). Empty = auto-detect")
	crashFlag := flag.Bool("crash", false, "Trigger synthetic panic for testing crash logging")
	logPathFlag := flag.String("logpath", "", "log directory path (default: OS-specific location, use ./ for current dir)")
	profileFlag := flag.String("profile", "", "Enable pprof profiling server (e.g., :6060 or localhost:6060)")
	testFlag := flag.Bool("test", false, "Test mode (headless, stdin-driven)")
	hybridFlag := flag.Bool("hybrid", false, "Enable hybrid tap+hold recording mode")
	longPressFlag := flag.Duration("longpress", 350*time.Millisecond, "Long-press threshold for PTT vs tap (e.g., 350ms)")
	tuiFlag := flag.Bool("tui", true, "Run with terminal UI")
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
	if activeTranscriber.Name() == "deepgram" {
		streamEnabled = true
	}
	if *langFlag != "" {
		activeTranscriber.SetLanguage(*langFlag)
	}

	// Resolve -setup into -device early (before daemonization)
	if *setupFlag && *deviceFlag == "" {
		ctx, err := audio.NewContext()
		if err != nil {
			fmt.Printf("Error initializing audio: %v\n", err)
			os.Exit(1)
		}
		if dev, _ := selectDevice(ctx); dev != nil {
			*deviceFlag = dev.Name
		}
		ctx.Close()
	}

	// Daemonize in non-TUI mode: re-exec in background, return shell prompt
	if !*tuiFlag && os.Getenv("_ZEE_BG") == "" {
		args := os.Args[1:]
		if *deviceFlag != "" {
			args = append(args, "-device", *deviceFlag)
		}
		exe, _ := os.Executable()
		cmd := exec.Command(exe, args...)
		cmd.Env = append(os.Environ(), "_ZEE_BG=1")
		devnull, _ := os.Open(os.DevNull)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Enable diagnostic logging in tray mode (always) or expert TUI mode
	if !*tuiFlag || *expertFlag {
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
	if *deviceFlag != "" {
		if devices, err := ctx.Devices(); err == nil {
			for i := range devices {
				if devices[i].Name == *deviceFlag {
					selectedDevice = &devices[i]
					break
				}
			}
		}
	} else if *setupFlag {
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
	if !*tuiFlag {
		tuiReadyOnce.Do(func() { close(tuiReady) })
	} else {
		tuiMu.Lock()
		tuiProgram = NewTUIProgram(*expertFlag)
		tuiMu.Unlock()

		go func() {
			if _, err := tuiProgram.Run(); err != nil {
				log.Errorf("TUI error: %v", err)
				os.Exit(1)
			}
			gracefulShutdown()
		}()

		<-tuiReady
	}

	tray.OnCopyLast(func() {
		transcriptionsMu.Lock()
		text := lastText
		transcriptionsMu.Unlock()
		if text != "" {
			clipboard.Copy(text)
		}
	})
	tray.OnRecord(
		func() { select { case trayRecordChan <- struct{}{}: default: } },
		func() { fireTrayStop() },
	)
	// preferredDevice remembers the user's choice so we can auto-reconnect
	preferredDevice := ""
	if selectedDevice != nil {
		preferredDevice = selectedDevice.Name
	}
	tray.SetBTCheck(audio.IsBluetooth)
	if devices, err := ctx.Devices(); err == nil && len(devices) > 0 {
		names := make([]string, len(devices))
		for i := range devices {
			names[i] = devices[i].Name
		}
		tray.SetDevices(names, preferredDevice, func(name string) {
			preferredDevice = name
			switchDeviceByName(ctx, captureConfig, &captureDevice, &selectedDevice, name)
		})
	}
	tray.SetAutoPaste(autoPaste)

	groqKey := os.Getenv("GROQ_API_KEY")
	dgKey := os.Getenv("DEEPGRAM_API_KEY")
	tray.SetProviders([]tray.Provider{
		{Name: "groq", Label: "Groq", HasKey: groqKey != "", Active: activeTranscriber.Name() == "groq"},
		{Name: "deepgram", Label: "Deepgram (stream)", HasKey: dgKey != "", Active: activeTranscriber.Name() == "deepgram"},
	}, func(name string) {
		switch name {
		case "groq":
			activeTranscriber = transcriber.NewGroq(groqKey)
			streamEnabled = false
			activeFormat = *formatFlag
		case "deepgram":
			activeTranscriber = transcriber.NewDeepgram(dgKey)
			streamEnabled = true
		}
		if lang := *langFlag; lang != "" {
			activeTranscriber.SetLanguage(lang)
		}
		tuiSend(ModeLineMsg{Text: modeLineText()})
	})

	tray.SetLanguage(*langFlag, func(code string) {
		activeTranscriber.SetLanguage(code)
		tuiSend(ModeLineMsg{Text: modeLineText()})
	})

	trayQuit := tray.Init()
	tray.OnAutoPaste(func(on bool) { autoPaste = on })

	// Poll for device changes (hotplug)
	go func() {
		var last []string
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			devices, err := ctx.Devices()
			if err != nil {
				continue
			}
			names := make([]string, len(devices))
			for i := range devices {
				names[i] = devices[i].Name
			}
			if slices.Equal(last, names) {
				continue
			}
			last = names
			selName := ""
			if selectedDevice != nil {
				selName = selectedDevice.Name
			}
			if selName != "" && !slices.Contains(names, selName) {
				// Selected device disappeared — fall back to default
				log.Info("device_disconnected: " + selName)
				applyDeviceSwitch(ctx, captureConfig, &captureDevice, &selectedDevice, nil)
				selName = ""
			} else if selName == "" && preferredDevice != "" && slices.Contains(names, preferredDevice) {
				// Preferred device reappeared — auto-reconnect
				log.Info("device_reconnected: " + preferredDevice)
				switchDeviceByName(ctx, captureConfig, &captureDevice, &selectedDevice, preferredDevice)
				selName = preferredDevice
			}
			tray.RefreshDevices(names, selName)
		}
	}()

	update.StartBackgroundCheck(version, log.Dir(), func(rel update.Release) {
		log.Info("update_available: " + rel.Version)
		tuiSend(UpdateAvailableMsg{Version: rel.Version})
		tray.SetUpdateAvailable(rel.Version)
	})

	sigChan := make(chan os.Signal, 1)
	shutdown.Notify(sigChan)
	go func() {
		select {
		case <-sigChan:
		case <-trayQuit:
		}
		gracefulShutdown()
	}()

	go beep.Init()

	hk := hotkey.New()
	if err := hk.Register(); err != nil {
		log.Errorf("hotkey register error: %v", err)
		fmt.Printf("Error registering hotkey: %v\n", err)
		os.Exit(1)
	}
	defer hk.Unregister()

	tuiSend(ModeLineMsg{Text: modeLineText()})
	tuiSend(DeviceLineMsg{Text: deviceLineText(selectedDevice)})
	tuiSend(BluetoothWarningMsg{IsBT: selectedDevice != nil && audio.IsBluetooth(selectedDevice.Name)})
	tuiSend(HybridHelpMsg{Enabled: *hybridFlag})

	logRecordDevice := func() {
		log.Info("recording_device: " + captureDevice.DeviceName())
	}

	startTrayRecording := func() {
		log.Info("tray_record_start")
		logRecordDevice()
		tuiSend(RecordingStartMsg{})
		tray.SetRecording(true)
		go beep.PlayStart()

		stop := mergeStop(newTrayStop())
		_, err := handleRecording(captureDevice, stop, nil)
		tray.SetRecording(false)
		reportRecordingError(err)
	}

	if *hybridFlag {
		hy := hotkey.NewHybrid(hk, *longPressFlag)
		for {
			select {
			case ev := <-hy.Start():
				log.Info("hotkey_start_" + string(ev.Mode))
				logRecordDevice()
				tuiSend(RecordingStartMsg{})
				tray.SetRecording(true)
				go beep.PlayStart()

				stop := mergeStop(hy.StopChan(), newTrayStop())
				_, err := handleRecording(captureDevice, stop, hy.IsToggle)
				tray.SetRecording(false)
				reportRecordingError(err)

			case <-trayRecordChan:
				startTrayRecording()

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
				tray.SetRecording(true)
				go beep.PlayStart()

				stop := mergeStop(hk.Keyup(), newTrayStop())
				_, err := handleRecording(captureDevice, stop, nil)
				tray.SetRecording(false)
				reportRecordingError(err)

			case <-trayRecordChan:
				startTrayRecording()

			case <-deviceSelectChan:
				handleDeviceSwitch(ctx, captureConfig, &captureDevice, &selectedDevice)
			}
		}
	}
}

func handleDeviceSwitch(ctx audio.Context, captureConfig audio.CaptureConfig, captureDevice *audio.CaptureDevice, selectedDevice **audio.DeviceInfo) {
	if tuiProgram != nil {
		tuiProgram.ReleaseTerminal()
	}
	newDevice, err := selectDevice(ctx)
	if tuiProgram != nil {
		tuiProgram.RestoreTerminal()
	}

	if err != nil {
		log.Warnf("device selection failed: %v", err)
		return
	}
	if newDevice != nil {
		applyDeviceSwitch(ctx, captureConfig, captureDevice, selectedDevice, newDevice)
	}
}

func switchDeviceByName(ctx audio.Context, captureConfig audio.CaptureConfig, captureDevice *audio.CaptureDevice, selectedDevice **audio.DeviceInfo, name string) {
	devices, err := ctx.Devices()
	if err != nil {
		log.Warnf("device enumeration failed: %v", err)
		return
	}
	for i := range devices {
		if devices[i].Name == name {
			applyDeviceSwitch(ctx, captureConfig, captureDevice, selectedDevice, &devices[i])
			return
		}
	}
	log.Warnf("device not found: %s", name)
}

func applyDeviceSwitch(ctx audio.Context, captureConfig audio.CaptureConfig, captureDevice *audio.CaptureDevice, selectedDevice **audio.DeviceInfo, newDevice *audio.DeviceInfo) {
	name := "system default"
	if newDevice != nil {
		name = newDevice.Name
	}
	log.Info("device_switch: " + name)
	(*captureDevice).Close()
	newCapture, err := ctx.NewCapture(newDevice, captureConfig)
	if err != nil {
		log.Errorf("capture device reinit error: %v", err)
		return
	}
	*captureDevice = newCapture
	*selectedDevice = newDevice
	tuiSend(DeviceLineMsg{Text: deviceLineText(newDevice)})
	tuiSend(BluetoothWarningMsg{IsBT: newDevice != nil && audio.IsBluetooth(newDevice.Name)})
}

func handleRecording(capture audio.CaptureDevice, stop <-chan struct{}, isToggleFn func() bool) (<-chan struct{}, error) {
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

	updatesDone := make(chan struct{})
	go func() {
		defer close(updatesDone)
		var prev string
		for text := range sess.Updates() {
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

	totalFrames, silenceClose, err := record(capture, stop, sess, isToggleFn)

	if err != nil {
		sess.Close()
		return nil, err
	}
	if totalFrames < uint64(encoder.SampleRate/10) {
		sess.Close()
		return nil, nil
	}

	recDur := time.Duration(float64(totalFrames) / float64(encoder.SampleRate) * float64(time.Second))
	done := make(chan struct{})
	go func() {
		finishTranscription(sess, clipCh, updatesDone, silenceClose, recDur)
		close(done)
	}()
	return done, nil
}

func finishTranscription(sess transcriber.Session, clipCh chan string, updatesDone <-chan struct{}, skipPaste bool, recDur time.Duration) {
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
		tray.SetError(closeErr.Error())
	}

	if closeErr == nil && !streamEnabled && result.HasText && autoPaste && !skipPaste {
		clipboard.Copy(result.Text)
		clipboard.Paste()
	}

	if autoPaste && !skipPaste && clipPrev != "" {
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
		log.Info("rate_limit: " + result.RateLimit)
		tuiSend(RateLimitMsg{Text: "requests: " + result.RateLimit + " remaining"})
	}

	if result.NoSpeech {
		log.Info("no_speech")
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
		transcriptionsMu.Lock()
		lastText = result.Text
		transcriptionsMu.Unlock()
		log.TranscriptionText(result.Text)
		tray.SetLastRecording(recDur)
	}
}

func record(capture audio.CaptureDevice, stop <-chan struct{}, sess transcriber.Session, isToggleFn func() bool) (uint64, bool, error) {
	vp, err := newVADProcessor()
	if err != nil {
		return 0, false, fmt.Errorf("VAD init: %w", err)
	}

	var bufMu sync.Mutex
	var totalFrames uint64
	var stopped bool
	var autoClosed atomic.Bool
	done := make(chan struct{})
	var closeOnce sync.Once
	closeDone := func() { closeOnce.Do(func() { close(done) }) }

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
			vp.Process(data)
		}
	})

	if err := capture.Start(); err != nil {
		capture.ClearCallback()
		return 0, false, err
	}

	isToggle := func() bool {
		return isToggleFn != nil && isToggleFn()
	}

	mon := newSilenceMonitor(isToggle)
	recordStart := time.Now()
	go func() {
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				tuiSend(RecordingTickMsg{Duration: time.Since(recordStart).Seconds()})
				switch mon.Tick(vp.HasSpeechTick()) {
				case SilenceWarn:
					log.Info("no_voice_warning")
					tuiSend(NoVoiceWarningMsg{})
					tray.SetWarning(true)
					beep.PlayError()
				case SilenceWarnClear:
					tuiSend(VoiceClearedMsg{})
					tray.SetWarning(false)
				case SilenceRepeat:
					log.Info("silence_during_warning")
					tuiSend(NoVoiceWarningMsg{})
					beep.PlayError()
				case SilenceAutoClose:
					log.Info("silence_auto_close")
					tuiSend(SilenceAutoCloseMsg{})
					tray.SetRecording(false)
					go beep.PlayEnd()
					time.Sleep(recordTail)
					autoClosed.Store(true)
					closeDone()
					return
				}
			}
		}
	}()

	go func() {
		select {
		case <-stop:
		case <-done:
			return
		}
		log.Info("recording_stop")
		tuiSend(RecordingStopMsg{})
		tray.SetRecording(false)
		go beep.PlayEnd()
		if streamEnabled {
			time.Sleep(recordTail)
		}
		closeDone()
	}()
	<-done

	capture.Stop()
	capture.ClearCallback()

	bufMu.Lock()
	stopped = true
	frames := totalFrames
	bufMu.Unlock()

	return frames, autoClosed.Load(), nil
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
