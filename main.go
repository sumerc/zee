package main

import (
	"context"
	"encoding/json"
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
	"zee/clipboard"
	"zee/config"
	"zee/encoder"
	"zee/hotkey"
	"zee/log"
	"zee/login"
	"zee/setup"
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

type savedRecording struct {
	AudioData   []byte
	AudioFormat string
	Text        string
	Provider    string
	Model       string
	Timestamp   time.Time
}

var (
	lastRecMu sync.Mutex
	lastRec   *savedRecording
)

func trayModelState(s transcriber.ModelStatus) tray.ModelState {
	switch {
	case s.Ready:
		return tray.ModelReady
	case s.Downloadable:
		return tray.ModelNeedsDownload
	default:
		return tray.ModelUnavailable
	}
}

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
	hints     string
	autoPaste bool
}

var configMu sync.Mutex

// captureMu guards the live captureDevice, plus selectedDevice and
// preferredDevice, which the device-monitor goroutine and the tray device
// callback hot-swap on connect/disconnect while recordSessions reads the capture
// device for each new recording.
var captureMu sync.Mutex

var trayRecordChan = make(chan struct{}, 1)
var isRecording atomic.Bool

// isTranscribing is true while a recording has stopped but its transcription is
// still running. isRecording stays true across this phase too (so a re-press is
// blocked); isTranscribing distinguishes "stop the live recording" from "denied,
// transcription in progress" for the hotkey feedback.
var isTranscribing atomic.Bool

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

// requestStop stops the active recording (safe to call from any goroutine,
// multiple times). The Once/channel are touched under stopMu so this can't race
// with resetStop resetting them for the next session; close() is non-blocking
// and never re-enters stopMu, so holding it here is safe.
func requestStop() {
	stopMu.Lock()
	defer stopMu.Unlock()
	if stopCh != nil {
		stopOnce.Do(func() { close(stopCh) })
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
		fmt.Println("Install:   curl -fsSL https://raw.githubusercontent.com/sumerc/zee/main/install.sh | bash")
		fmt.Printf("Download:  %s\n", rel.URL)
		os.Exit(0)
	}

	benchmarkFile := flag.String("benchmark", "", "Run benchmark with WAV file instead of live recording")
	benchmarkRuns := flag.Int("runs", 3, "Number of benchmark iterations")
	autoPasteFlag := flag.Bool("autopaste", true, "Auto-paste to focused window after transcription")
	setupFlag := flag.Bool("setup", false, "Run the interactive setup wizard (provider, key, device, permissions, hotkey) and exit")
	deviceFlag := flag.String("device", "", "Use named microphone device")
	formatFlag := flag.String("format", "mp3@16", "Audio format: mp3@16, mp3@64, or flac")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	debugFlag := flag.Bool("debug", true, "Enable diagnostic logging (timing, errors, events)")
	debugTranscribeFlag := flag.Bool("debug-transcribe", false, "Enable transcription text logging")
	langFlag := flag.String("lang", "en", "Language code for transcription (e.g., en, es, fr). Empty = auto-detect")
	logPathFlag := flag.String("logpath", "", "log directory path (default: OS-specific location, use ./ for current dir)")
	testFlag := flag.Bool("test", false, "Test mode (headless, stdin-driven)")
	hintsFlag := flag.String("hints", "", "Vocabulary hints for transcription (comma-separated)")
	transcribeFlag := flag.String("transcribe", "", "Transcribe audio file(s) and exit; extra files may follow as positional args (one transcript printed per line)")
	providerFlag := flag.String("provider", "", "Transcription provider (e.g. parakeet, groq); overrides saved config")
	modelFlag := flag.String("model", "", "Model ID for the selected provider; overrides saved config")
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

	if pprofAddr := os.Getenv("ZEE_PPROF"); pprofAddr != "" {
		go func() {
			fmt.Fprintf(os.Stderr, "pprof server listening on http://%s/debug/pprof/\n", pprofAddr)
			if err := http.ListenAndServe(pprofAddr, nil); err != nil {
				fmt.Fprintf(os.Stderr, "pprof server error: %v\n", err)
			}
		}()
	}

	if os.Getenv("ZEE_CRASH") == "1" {
		panic("TEST CRASH: synthetic panic to verify crash logging")
	}

	if *versionFlag {
		fmt.Printf("zee %s\n", version)
		os.Exit(0)
	}

	// Cloud provider API keys come from credentials.json (via config.APIKey),
	// never the environment. Wire the resolver before any provider is used —
	// including the setup wizard below, which resolves a transcriber.
	transcriber.SetKeySource(config.APIKey)

	// The setup wizard is a self-contained mode: it configures provider/key,
	// device, permissions and hotkey, then launches the app and exits. It does
	// its own config load and (on macOS) re-execs as the bundle for TCC.
	if *setupFlag {
		os.Exit(setup.Run())
	}
	// Load persistent settings, merge with CLI flags
	if err := config.Load(); err != nil {
		log.Warnf("settings: %v", err)
	}
	cfg := config.Get()
	flagSet := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { flagSet[f.Name] = true })
	if !flagSet["lang"] {
		// Apply the saved language even when empty — "" is Auto-detect, a real
		// choice, not "unset". A never-configured config yields "en" via defaults.
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
	// Validate format
	switch *formatFlag {
	case "mp3@16", "mp3@64", "flac":
		activeFormat = *formatFlag
		if *hintsFlag != "" {
			config.SetHints(*hintsFlag)
		}
	default:
		fatal("Unknown format %q (use mp3@16, mp3@64, or flac)", *formatFlag)
	}

	// CLI -provider/-model override the saved provider/model (also lets the
	// integration test pick a specific local model).
	if flagSet["provider"] {
		cfg.Provider = *providerFlag
	}
	if flagSet["model"] {
		cfg.Model = *modelFlag
	}

	// Restore saved provider/model or fall back to auto-detection
	if cfg.Provider != "" {
		if p, ok := providerByName(cfg.Provider); ok && p.Available() {
			activeTranscriber = p.New()
			if cfg.Model != "" {
				activeTranscriber.SetModel(cfg.Model)
			}
		}
		// An explicit -provider that didn't resolve is a hard error (don't
		// silently fall back to a different engine under the test's feet).
		if activeTranscriber == nil && flagSet["provider"] {
			fatal("Provider %q is not available", *providerFlag)
		}
	}
	if activeTranscriber == nil {
		var initErr error
		activeTranscriber, initErr = transcriber.New()
		if initErr != nil {
			fatal("%v", initErr)
		}
	}
	streamEnabled = modelSupportsStream(activeTranscriber)
	if *langFlag != "" {
		activeTranscriber.SetLanguage(*langFlag)
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

	if *transcribeFlag != "" {
		// First file is the flag value; any remaining positionals are extra
		// files transcribed in the same process (the model loads once).
		runTranscribeFiles(append([]string{*transcribeFlag}, flag.Args()...))
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
		func() {
			select {
			case trayRecordChan <- struct{}{}:
			default:
			}
		},
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
			captureMu.Lock()
			preferredDevice = name
			captureMu.Unlock()
			config.Update(func(s *config.Settings) { s.Device = name })
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
		for _, m := range p.Models {
			st := p.Status(m.ID)
			trayModels = append(trayModels, tray.Model{
				Provider:      p.Name,
				ProviderLabel: p.Label,
				ModelID:       m.ID,
				Label:         m.Label,
				State:         trayModelState(st),
				Detail:        st.Detail,
				Active:        activeTranscriber.Name() == p.Name && activeTranscriber.GetModel() == m.ID,
			})
			modelIndex[p.Name+":"+m.ID] = m
		}
	}

	tray.SetLanguages(activeTranscriber.SupportedLanguages())

	// applySwitch makes (provider, model) active, reusing the current instance
	// when the provider is unchanged so we don't reload a local model twice. On a
	// provider change it frees the outgoing model — Parakeet holds C/ggml memory
	// (255 MB–1.4 GB) the GC can't reclaim, so dropping it without Close leaks.
	// It must run only when no record/inference cycle is active (guaranteed by
	// switchModel), so the freed model can't be one an in-flight session uses.
	applySwitch := func(p transcriber.ProviderInfo, model string) {
		configMu.Lock()
		var outgoing transcriber.Transcriber
		if activeTranscriber.Name() != p.Name {
			outgoing = activeTranscriber
			newTr := p.New()
			newTr.SetLanguage(activeTranscriber.GetLanguage())
			activeTranscriber = newTr
		}
		activeTranscriber.SetModel(model) // local: blocks here during gguf load
		streamEnabled = modelIndex[p.Name+":"+model].Stream
		if !streamEnabled {
			activeFormat = *formatFlag
		}
		langs := activeTranscriber.SupportedLanguages()
		local := transcriber.IsLocal(activeTranscriber)
		configMu.Unlock()

		// Only Parakeet has a provider-level Close (frees the gguf); cloud
		// providers don't implement it and are skipped.
		if c, ok := outgoing.(interface{ Close() }); ok {
			c.Close()
		}

		config.Update(func(s *config.Settings) { s.Provider = p.Name; s.Model = model })
		tray.SetLanguages(langs)
		tray.SetHintsEnabled(!local)
		tray.SetActiveModel(p.Name, model)
	}

	// switchModel applies the swap immediately when idle, or defers it to the end
	// of the current record/inference cycle when one is active — so neither the
	// gguf reload nor the Close of the outgoing model can free a ctx an in-flight
	// session is using. The tray menu and the download-complete goroutine both
	// funnel through here.
	switchModel := func(p transcriber.ProviderInfo, model string) {
		if isRecording.Load() {
			pendingMu.Lock()
			pendingSwitch = func() { applySwitch(p, model) }
			pendingMu.Unlock()
			return
		}
		applySwitch(p, model)
	}

	tray.SetModels(trayModels, func(provider, model string) {
		p, ok := providerByName(provider)
		if !ok {
			return
		}
		st := p.Status(model)
		switch {
		case st.Ready:
			switchModel(p, model)
		case st.Downloadable:
			// Async: a model download takes minutes; show progress in the menu.
			go func() {
				tray.UpdateModelState(provider, model, tray.ModelDownloading, "0%")
				err := p.Download(model, func(f float64) {
					tray.UpdateModelState(provider, model, tray.ModelDownloading, fmt.Sprintf("%.0f%%", f*100))
				})
				if err != nil {
					log.Errorf("model download: %v", err)
					tray.SetError("Download failed: " + err.Error())
					tray.UpdateModelState(provider, model, tray.ModelNeedsDownload, st.Detail)
					return
				}
				tray.UpdateModelState(provider, model, tray.ModelReady, "")
				switchModel(p, model)
			}()
		}
	})

	tray.SetLanguage(*langFlag, func(code string, persist bool) {
		configMu.Lock()
		activeTranscriber.SetLanguage(code)
		configMu.Unlock()
		// Only a real user choice persists. A model-constraint fallback applies
		// to the transcriber but must not overwrite the saved preference.
		if persist {
			config.Update(func(s *config.Settings) { s.Language = code })
		}
	})
	tray.SetHintsEnabled(!transcriber.IsLocal(activeTranscriber))
	tray.SetLogin(login.Enabled())
	tray.SetVersion(version)
	tray.OnSaveAudio(saveLastRecording)
	tray.OnEditHints(func() {
		exec.Command("open", config.HintsPath()).Run()
	})
	tray.OnEditSettings(func() {
		config.EnsureSaved() // config.json may not exist yet; open needs a file
		exec.Command("open", "-t", config.SettingsPath()).Run()
	})
	tray.SetHotkeyLabel(currentHotkeyCombo(cfg).Label)

	trayQuit := tray.Init()
	tray.OnAutoPaste(func(on bool) {
		configMu.Lock()
		autoPaste = on
		configMu.Unlock()
		config.Update(func(s *config.Settings) { s.AutoPaste = on })
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
			config.Update(func(s *config.Settings) { s.AutoStart = on })
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
			// Snapshot the device state under captureMu, then release before the
			// switch calls (which re-lock it). preferredDevice is also written by
			// the tray callback on another thread, so both are read under the lock.
			captureMu.Lock()
			selName := ""
			if selectedDevice != nil {
				selName = selectedDevice.Name
			}
			pref := preferredDevice
			captureMu.Unlock()
			if selName != "" && !slices.Contains(names, selName) {
				log.Info("device_disconnected: " + selName)
				applyDeviceSwitch(ctx, captureConfig, &captureDevice, &selectedDevice, nil)
				selName = ""
			} else if selName == "" && pref != "" && slices.Contains(names, pref) {
				log.Info("device_reconnected: " + pref)
				switchDeviceByName(ctx, captureConfig, &captureDevice, &selectedDevice, pref)
				selName = pref
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
			installURL := "https://github.com/" + update.Repo + "#install"
			if alert.Confirm("Update available: "+version+" → "+rel.Version+"\n\nOne-liner install instructions:\n"+installURL, "Open Install Instructions") {
				exec.Command("open", installURL).Start()
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

	go audio.InitBeep()

	hk := hotkey.New(currentHotkeyCombo(cfg))
	if err := hk.Register(); err != nil {
		log.Errorf("hotkey register error: %v", err)
		fatal("Failed to register hotkey: %v\n\nGrant Accessibility access in System Settings → Privacy & Security.", err)
	}
	defer hk.Unregister()

	sessions := make(chan recSession, 1)
	go listenHotkey(hk, longPressDuration(), sessions)

	go func() {
		for range trayRecordChan {
			tryStartSession(sessions)
		}
	}()

	recordSessions(func() audio.CaptureDevice {
		captureMu.Lock()
		defer captureMu.Unlock()
		return captureDevice
	}, sessions)
}

// afterRecordCycle, when non-nil, is called by recordSessions at the end of each
// record+transcribe cycle. Test-only hook (lets the harness know a cycle ended).
var afterRecordCycle func()

// pendingSwitch holds a model switch deferred because it was requested during a
// record/inference cycle; applyPendingSwitch runs it at cycle end, when no
// session is in flight (see switchModel). pendingMu guards it across the tray and
// download goroutines and the record loop.
var (
	pendingMu     sync.Mutex
	pendingSwitch func()
)

func applyPendingSwitch() {
	pendingMu.Lock()
	fn := pendingSwitch
	pendingSwitch = nil
	pendingMu.Unlock()
	if fn != nil {
		fn()
	}
}

// tryStartSession enqueues a fresh recording session unless a cycle is already
// active (recording OR still transcribing) — in which case it denies audibly,
// the same guard the hotkey uses. Returns the SilenceClose handle when it
// started a session, nil when it denied. The hotkey and the tray "Start
// Recording" button both funnel through here, so neither can queue an
// unattended recording that fires the instant inference ends.
func tryStartSession(sessions chan<- recSession) *atomic.Bool {
	if isRecording.Load() {
		go audio.PlayDenied()
		return nil
	}
	sc := &atomic.Bool{}
	sessions <- recSession{Stop: resetStop(), SilenceClose: sc}
	return sc
}

// recordSessions is the core record→transcribe loop, shared by the live app and
// tests. isRecording stays true for the WHOLE cycle — recording AND inference —
// so listenHotkey blocks a new recording while a transcription is still running
// (handleRecording returns a `done` channel that closes when inference ends).
//
// getCapture is called fresh each iteration (not captured once) so a device
// hot-swap — e.g. the mic being unplugged and the monitor switching to system
// default — is picked up on the next recording instead of reusing a stale,
// now-invalid device.
func recordSessions(getCapture func() audio.CaptureDevice, sessions <-chan recSession) {
	for sess := range sessions {
		capture := getCapture()
		log.Info("recording_start")
		log.Info("recording_device: " + capture.DeviceName())
		isRecording.Store(true)
		tray.SetRecording(true)
		go audio.PlayStart()

		done, err := handleRecording(capture, sess)
		if err != nil {
			log.Errorf("recording error: %v", err)
			tray.SetError(err.Error())
		}
		if done != nil {
			isTranscribing.Store(true)
			tray.SetTranscribing(true) // blue status dot while inference runs
			<-done                     // hold isRecording too — blocks re-record
			isTranscribing.Store(false)
		}
		isRecording.Store(false)
		tray.SetRecording(false)
		applyPendingSwitch() // apply any model switch deferred during this cycle
		if afterRecordCycle != nil {
			afterRecordCycle()
		}
	}
}

// currentHotkeyCombo maps the saved config hotkey to a hotkey.Combo, falling
// back to the built-in default when nothing is saved.
func currentHotkeyCombo(cfg config.Settings) hotkey.Combo {
	c := hotkey.Combo{Mods: cfg.Hotkey.Mods, Key: cfg.Hotkey.Key, Label: cfg.Hotkey.Label}
	if c.IsZero() {
		return hotkey.DefaultCombo()
	}
	return c
}

func longPressDuration() time.Duration {
	const def = 350 * time.Millisecond
	if v := os.Getenv("ZEE_LONGPRESS_DURATION"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Warnf("invalid ZEE_LONGPRESS_DURATION %q, using default %s", v, def)
	}
	return def
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
				if isTranscribing.Load() {
					go audio.PlayDenied() // ignored: transcription still in progress
				} else {
					requestStop()
				}
				continue
			}
			sc := tryStartSession(sessions)
			if sc == nil {
				<-hk.Keyup() // denied (a cycle began between the guard above and here)
				continue
			}
			timer := time.NewTimer(longPress)
			select {
			case <-timer.C:
				<-hk.Keyup()
				requestStop()
				st = idle
			case <-hk.Keyup():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
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
	captureMu.Lock()
	defer captureMu.Unlock()
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
		hints:     config.GetHints(),
		autoPaste: autoPaste,
	}
	configMu.Unlock()

	tSess, err := cfg.tr.NewSession(context.Background(), transcriber.SessionConfig{
		Stream:   cfg.stream,
		Format:   cfg.format,
		Language: cfg.lang,
		Hints:    cfg.hints,
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
			ProcessRSSMB:     result.ProcessRSSMB,
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
			ProcessRSSMB: result.ProcessRSSMB,
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

	if len(result.AudioData) > 0 {
		lastRecMu.Lock()
		lastRec = &savedRecording{
			AudioData:   result.AudioData,
			AudioFormat: result.AudioFormat,
			Text:        result.Text,
			Provider:    cfg.tr.Name(),
			Model:       cfg.tr.GetModel(),
			Timestamp:   time.Now(),
		}
		lastRecMu.Unlock()
	}
}

func saveLastRecording() {
	lastRecMu.Lock()
	rec := lastRec
	lastRecMu.Unlock()

	if rec == nil {
		alert.Warn("No recording to save")
		return
	}

	ts := rec.Timestamp.Format("2006-01-02T15-04-05")
	dir := filepath.Join(config.Dir(), "samples", ts)
	if err := os.MkdirAll(dir, 0755); err != nil {
		alert.Error("Save failed: " + err.Error())
		return
	}

	ext := rec.AudioFormat
	if err := os.WriteFile(filepath.Join(dir, "audio."+ext), rec.AudioData, 0644); err != nil {
		alert.Error("Save failed: " + err.Error())
		return
	}

	info, _ := json.Marshal(map[string]string{
		"provider":  rec.Provider,
		"model":     rec.Model,
		"format":    rec.AudioFormat,
		"text":      rec.Text,
		"timestamp": rec.Timestamp.Format(time.RFC3339),
	})
	os.WriteFile(filepath.Join(dir, "info.json"), info, 0644)

	alert.Info("Saved to " + dir)
}

// directTranscriber transcribes encoded audio bytes in one call. Every provider
// implements it — cloud providers POST the bytes; Parakeet decodes WAV → PCM and
// runs a local batch inference — so the file path has a single shape.
type directTranscriber interface {
	Transcribe(audio []byte, format, lang, hints string) (*transcriber.Result, error)
}

// providerByName finds a registered provider by its Name.
func providerByName(name string) (transcriber.ProviderInfo, bool) {
	for _, p := range transcriber.Providers() {
		if p.Name == name {
			return p, true
		}
	}
	return transcriber.ProviderInfo{}, false
}

// runTranscribeFiles transcribes one or more files with the already-loaded
// engine — the model is loaded once at startup and reused across files — and
// prints one transcript per line, in input order.
func runTranscribeFiles(files []string) {
	for _, f := range files {
		text, err := transcribeFile(f)
		if err != nil {
			fatal("%s: %v", f, err)
		}
		fmt.Println(text)
	}
}

func transcribeFile(audioFile string) (string, error) {
	data, err := os.ReadFile(audioFile)
	if err != nil {
		return "", err
	}

	ext := filepath.Ext(audioFile)
	var format string
	switch ext {
	case ".flac":
		format = "flac"
	case ".wav":
		format = "wav"
	case ".mp3":
		format = "mp3"
	default:
		return "", fmt.Errorf("unsupported audio format %q", ext)
	}

	dt, ok := activeTranscriber.(directTranscriber)
	if !ok {
		return "", fmt.Errorf("provider %q cannot transcribe files", activeTranscriber.Name())
	}
	result, err := dt.Transcribe(data, format, activeTranscriber.GetLanguage(), config.GetHints())
	if err != nil {
		return "", err
	}
	return result.Text, nil
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
