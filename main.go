package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"zee/alert"
	"zee/audio"
	"zee/beep"
	"zee/clipboard"
	"zee/doctor"
	"zee/encoder"
	"zee/hotkey"
	"zee/log"
	"zee/login"
	"zee/shutdown"
	"zee/transcriber"
	"zee/tray"
	"zee/update"
)

var version = "dev"

func fatal(msg string, args ...any) {
	s := fmt.Sprintf(msg, args...)
	fmt.Fprintln(os.Stderr, s)
	alert.Error(s)
	os.Exit(1)
}

var activeTranscriber transcriber.Transcriber
var autoPaste bool
var transcriptionsMu sync.Mutex
var transcriptionCount int
var streamEnabled bool
var activeFormat string

func modelSupportsStream(tr transcriber.Transcriber) bool {
	id := tr.GetModel()
	for _, m := range tr.Models() {
		if m.ID == id {
			return m.Stream
		}
	}
	return false
}

type recSession struct {
	Stop         <-chan struct{}
	SilenceClose *atomic.Bool
}

type recordingConfig struct {
	tr        transcriber.Transcriber
	stream    bool
	format    string
	lang      string
	autoPaste bool
}

var configMu sync.Mutex

var trayRecordChan = make(chan struct{}, 1)
var isRecording atomic.Bool

var (
	stopMu   sync.Mutex
	stopCh   chan struct{} // closed to stop the active recording
	stopOnce sync.Once
)

// resetStop prepares a fresh stop channel for a new recording.
func resetStop() <-chan struct{} {
	stopMu.Lock()
	stopCh = make(chan struct{})
	stopOnce = sync.Once{}
	ch := stopCh
	stopMu.Unlock()
	return ch
}

// requestStop stops the active recording (safe to call from any goroutine, multiple times).
func requestStop() {
	stopMu.Lock()
	once := &stopOnce
	ch := stopCh
	stopMu.Unlock()
	if ch != nil {
		once.Do(func() { close(ch) })
	}
}

var shutdownOnce sync.Once

func gracefulShutdown() {
	shutdownOnce.Do(func() {
		transcriptionsMu.Lock()
		n := transcriptionCount
		transcriptionsMu.Unlock()
		if n > 0 {
			log.SessionEnd(n)
		}
		log.Close()
		tray.Quit()
		os.Exit(0)
	})
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
		fmt.Printf("\nUpdate available: %s → %s\n\n", version, rel.Version)
		fmt.Println("Homebrew:  brew upgrade sumerc/tap/zee")
		fmt.Printf("Download:  %s\n", rel.URL)
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
	debugFlag := flag.Bool("debug", true, "Enable diagnostic logging (timing, errors, events)")
	debugTranscribeFlag := flag.Bool("debug-transcribe", false, "Enable transcription text logging")
	langFlag := flag.String("lang", "en", "Language code for transcription (e.g., en, es, fr). Empty = auto-detect")
	crashFlag := flag.Bool("crash", false, "Trigger synthetic panic for testing crash logging")
	logPathFlag := flag.String("logpath", "", "log directory path (default: OS-specific location, use ./ for current dir)")
	profileFlag := flag.String("profile", "", "Enable pprof profiling server (e.g., :6060 or localhost:6060)")
	testFlag := flag.Bool("test", false, "Test mode (headless, stdin-driven)")
	longPressFlag := flag.Duration("longpress", 350*time.Millisecond, "Long-press threshold for PTT vs tap (e.g., 350ms)")
	flag.Parse()

	// Resolve log directory early
	logPath, err := log.ResolveDir(*logPathFlag)
	if err != nil {
		fatal("Failed to resolve log directory: %v", err)
	}
	log.SetDir(logPath)

	if err := log.EnsureDir(); err != nil {
		log.Warnf("could not create log directory: %v", err)
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
	// Load persistent settings, merge with CLI flags
	if err := loadSettings(); err != nil {
		log.Warnf("settings: %v", err)
	}
	cfg := getSettings()
	flagSet := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { flagSet[f.Name] = true })
	if !flagSet["lang"] && cfg.Language != "" {
		*langFlag = cfg.Language
	}
	if !flagSet["device"] && cfg.Device != "" {
		*deviceFlag = cfg.Device
	}
	if !flagSet["autopaste"] {
		autoPaste = cfg.AutoPaste
	} else {
		autoPaste = *autoPasteFlag
	}
	streamEnabled = *streamFlag

	// Validate format
	switch *formatFlag {
	case "mp3@16", "mp3@64", "flac":
		activeFormat = *formatFlag
	default:
		fatal("Unknown format %q (use mp3@16, mp3@64, or flac)", *formatFlag)
	}

	if streamEnabled && *formatFlag != "mp3@16" {
		log.Warn("format ignored in streaming mode")
	}

	// Restore saved provider/model or fall back to auto-detection
	if cfg.Provider != "" {
		for _, p := range transcriber.Providers() {
			if p.Name == cfg.Provider {
				if key := os.Getenv(p.EnvKey); key != "" {
					activeTranscriber = p.NewFn(key)
					if cfg.Model != "" {
						activeTranscriber.SetModel(cfg.Model)
					}
				}
				break
			}
		}
	}
	if activeTranscriber == nil {
		var initErr error
		activeTranscriber, initErr = transcriber.New()
		if initErr != nil {
			fatal("No API key set.\n\nSet GROQ_API_KEY, OPENAI_API_KEY, or DEEPGRAM_API_KEY.")
		}
	}
	streamEnabled = modelSupportsStream(activeTranscriber)
	if *langFlag != "" {
		activeTranscriber.SetLanguage(*langFlag)
	}

	if *setupFlag && *deviceFlag == "" {
		ctx, err := audio.NewContext()
		if err != nil {
			fatal("Error initializing audio: %v", err)
		}
		if dev, _ := selectDevice(ctx); dev != nil {
			*deviceFlag = dev.Name
		}
		ctx.Close()
	}

	if *debugFlag {
		log.SetTranscribeEnabled(*debugTranscribeFlag)
		if err := log.Init(); err != nil {
			alert.Warn("Debug logging will not work.\n\n" + err.Error())
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
			log.Warnf("paste init failed: %v", err)
			alert.Warn("Auto-paste will not work.\n\n" + err.Error())
		}
		if !clipboard.CheckAccessibility() {
			alert.Warn("Auto-paste requires Accessibility permission.\n\nGrant access to Zee.app (or your terminal app if running from CLI) in:\nSystem Settings → Privacy & Security → Accessibility")
			exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility").Start()
		}
	}

	ctx, err := audio.NewContext()
	if err != nil {
		log.Errorf("audio context init error: %v", err)
		fatal("Failed to initialize audio: %v", err)
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
			log.Warnf("device selection failed: %v — falling back to default", err)
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
		fatal("Failed to initialize microphone: %v", err)
	}
	defer captureDevice.Close()

	tray.OnCopyLast(clip.CopyLast)
	tray.OnRecord(
		func() { select { case trayRecordChan <- struct{}{}: default: } },
		func() { requestStop() },
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
			updateSettings(func(s *Settings) { s.Device = name })
			if name == "" {
				applyDeviceSwitch(ctx, captureConfig, &captureDevice, &selectedDevice, nil)
			} else {
				switchDeviceByName(ctx, captureConfig, &captureDevice, &selectedDevice, name)
			}
		})
	}
	tray.SetAutoPaste(autoPaste)

	var trayModels []tray.Model
	modelIndex := map[string]transcriber.ModelInfo{}
	for _, p := range transcriber.Providers() {
		key := os.Getenv(p.EnvKey)
		for _, m := range p.Models {
			trayModels = append(trayModels, tray.Model{
				Provider:      p.Name,
				ProviderLabel: p.Label,
				ModelID:       m.ID,
				Label:         m.Label,
				HasKey:        key != "",
				Active:        activeTranscriber.Name() == p.Name && activeTranscriber.GetModel() == m.ID,
			})
			modelIndex[p.Name+":"+m.ID] = m
		}
	}

	tray.SetLanguages(transcriber.AllLanguages())

	tray.SetModels(trayModels, func(provider, model string) {
		configMu.Lock()
		defer configMu.Unlock()

		currentLang := activeTranscriber.GetLanguage()

		var newTr transcriber.Transcriber
		for _, p := range transcriber.Providers() {
			if p.Name == provider {
				if key := os.Getenv(p.EnvKey); key != "" {
					newTr = p.NewFn(key)
				}
				break
			}
		}
		if newTr == nil {
			return
		}
		newTr.SetLanguage(currentLang)
		newTr.SetModel(model)

		activeTranscriber = newTr
		streamEnabled = modelIndex[provider+":"+model].Stream
		if !streamEnabled {
			activeFormat = *formatFlag
		}

		updateSettings(func(s *Settings) { s.Provider = provider; s.Model = model })
		tray.SetLanguages(newTr.SupportedLanguages())
	})

	tray.SetLanguage(*langFlag, func(code string) {
		configMu.Lock()
		activeTranscriber.SetLanguage(code)
		configMu.Unlock()
		updateSettings(func(s *Settings) { s.Language = code })
	})
	tray.SetLogin(login.Enabled())
	tray.SetVersion(version)

	trayQuit := tray.Init()
	tray.OnAutoPaste(func(on bool) {
		configMu.Lock()
		autoPaste = on
		configMu.Unlock()
		updateSettings(func(s *Settings) { s.AutoPaste = on })
	})
	tray.OnLogin(func(on bool) error {
		var err error
		if on {
			err = login.Enable()
		} else {
			err = login.Disable()
		}
		if err != nil {
			log.Errorf("login toggle: %v", err)
			tray.SetError(err.Error())
		} else {
			updateSettings(func(s *Settings) { s.AutoStart = on })
		}
		return err
	})

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
				log.Info("device_disconnected: " + selName)
				applyDeviceSwitch(ctx, captureConfig, &captureDevice, &selectedDevice, nil)
				selName = ""
			} else if selName == "" && preferredDevice != "" && slices.Contains(names, preferredDevice) {
				log.Info("device_reconnected: " + preferredDevice)
				switchDeviceByName(ctx, captureConfig, &captureDevice, &selectedDevice, preferredDevice)
				selName = preferredDevice
			}
			tray.RefreshDevices(names, selName)
		}
	}()

	tray.OnCheckUpdate(func() {
		go func() {
			rel, err := update.CheckLatest(version)
			if err != nil {
				alert.Warn("Could not check for updates:\n" + err.Error())
				return
			}
			if rel == nil {
				alert.Info("You're on the latest version (" + version + ")")
				return
			}
			if alert.Confirm("Update available: "+version+" → "+rel.Version+"\n\nHomebrew:\nbrew upgrade sumerc/tap/zee", "Open Release Page") {
				exec.Command("open", rel.URL).Start()
			}
		}()
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
		fatal("Failed to register hotkey: %v\n\nGrant Accessibility access in System Settings → Privacy & Security.", err)
	}
	defer hk.Unregister()

	logRecordDevice := func() {
		log.Info("recording_device: " + captureDevice.DeviceName())
	}

	sessions := make(chan recSession, 1)
	go listenHotkey(hk, *longPressFlag, sessions)

	go func() {
		for range trayRecordChan {
			sessions <- recSession{Stop: resetStop(), SilenceClose: &atomic.Bool{}}
		}
	}()

	for sess := range sessions {
		log.Info("recording_start")
		logRecordDevice()
		isRecording.Store(true)
		tray.SetRecording(true)
		go beep.PlayStart()

		_, err := handleRecording(captureDevice, sess)
		isRecording.Store(false)
		tray.SetRecording(false)
		if err != nil {
			log.Errorf("recording error: %v", err)
			tray.SetError(err.Error())
		}
	}
}

func listenHotkey(hk hotkey.Hotkey, longPress time.Duration, sessions chan<- recSession) {
	type state int
	const (
		idle state = iota
		toggleRecording
	)

	st := idle
	for {
		switch st {
		case idle:
			<-hk.Keydown()
			if isRecording.Load() {
				<-hk.Keyup()
				requestStop()
				continue
			}
			sc := &atomic.Bool{}
			sessions <- recSession{Stop: resetStop(), SilenceClose: sc}
			timer := time.NewTimer(longPress)
			select {
			case <-timer.C:
				<-hk.Keyup()
				requestStop()
				st = idle
			case <-hk.Keyup():
				if !timer.Stop() { select { case <-timer.C: default: } }
				sc.Store(true)
				st = toggleRecording
			}
		case toggleRecording:
			<-hk.Keydown()
			<-hk.Keyup()
			requestStop()
			st = idle
		}
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
}

func handleRecording(capture audio.CaptureDevice, sess recSession) (<-chan struct{}, error) {
	clip.CancelRestore()

	configMu.Lock()
	cfg := recordingConfig{
		tr:        activeTranscriber,
		stream:    streamEnabled,
		format:    activeFormat,
		lang:      activeTranscriber.GetLanguage(),
		autoPaste: autoPaste,
	}
	configMu.Unlock()

	tSess, err := cfg.tr.NewSession(context.Background(), transcriber.SessionConfig{
		Stream:   cfg.stream,
		Format:   cfg.format,
		Language: cfg.lang,
	})
	if err != nil {
		return nil, err
	}

	// Save clipboard before recording overwrites it (async to not delay capture start)
	clipCh := make(chan string, 1)
	if cfg.autoPaste {
		go func() { clipCh <- clip.SaveCurrent() }()
	}

	updatesDone := make(chan struct{})
	go func() {
		defer close(updatesDone)
		var prev string
		for text := range tSess.Updates() {
			if cfg.autoPaste && len(text) > len(prev) {
				clip.PasteText(text[len(prev):])
			}
			prev = text
		}
	}()

	rec, err := newRecordingSession(capture, sess.Stop, tSess, sess.SilenceClose, cfg.stream)
	if err != nil {
		tSess.Close()
		return nil, err
	}
	if err := rec.Start(); err != nil {
		tSess.Close()
		return nil, err
	}
	rec.Wait()

	if rec.totalFrames < uint64(encoder.SampleRate/10) {
		tSess.Close()
		return nil, nil
	}

	recDur := time.Duration(float64(rec.totalFrames) / float64(encoder.SampleRate) * float64(time.Second))
	done := make(chan struct{})
	go func() {
		finishTranscription(tSess, clipCh, updatesDone, rec.autoClosed.Load(), recDur, cfg)
		close(done)
	}()
	return done, nil
}

func finishTranscription(sess transcriber.Session, clipCh chan string, updatesDone <-chan struct{}, skipPaste bool, recDur time.Duration, cfg recordingConfig) {
	result, closeErr := sess.Close()
	<-updatesDone

	var clipPrev string
	if cfg.autoPaste {
		clipPrev = <-clipCh
	}

	if closeErr != nil {
		log.Errorf("transcription error: %v", closeErr)
		tray.SetError(closeErr.Error())
	}

	if closeErr == nil && !cfg.stream && result.HasText && cfg.autoPaste && !skipPaste {
		clip.PasteText(result.Text)
	}

	if cfg.autoPaste && !skipPaste {
		clip.ScheduleRestore(clipPrev)
	}

	if closeErr != nil {
		return
	}

	if result.RateLimit != "" && result.RateLimit != "?/?" {
		log.Info("rate_limit: " + result.RateLimit)
	}

	if result.NoSpeech {
		log.Info("no_speech")
	}

	if result.Batch != nil {
		bs := result.Batch
		m := log.Metrics{
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
			InferenceMs:      bs.InferenceMs,
		}
		transcriptionsMu.Lock()
		transcriptionCount++
		transcriptionsMu.Unlock()
		log.TranscriptionMetrics(m, cfg.format, cfg.format, cfg.tr.Name(), bs.ConnReused, bs.TLSProtocol)
		log.Confidence(bs.Confidence)
	}

	if result.Stream != nil {
		ss := result.Stream
		log.StreamMetrics(log.StreamMetricsData{
			Provider:     cfg.tr.Name(),
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
		clip.SetLastText(result.Text)
		log.TranscriptionText(result.Text)
		var totalMs float64
		if result.Batch != nil {
			totalMs = result.Batch.TotalTimeMs
		} else if result.Stream != nil {
			totalMs = result.Stream.TotalMs
		}
		tray.SetLastRecording(recDur, totalMs)
	}
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
