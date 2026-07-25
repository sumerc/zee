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
	"zee/permissions"
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
	Err         string
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

func deviceNames(devices []audio.DeviceInfo) []string {
	names := make([]string, len(devices))
	for i := range devices {
		names[i] = devices[i].Name
	}
	return names
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
	PressedAt    time.Time // when the press was accepted; drives the reflex-latency metric
}

type recordingConfig struct {
	tr              transcriber.Transcriber
	stream          bool
	format          string
	lang            string
	hints           string
	autoPaste       bool
	tailWait        time.Duration // mic kept open after release so a fast keyup doesn't clip the last word
	pressToRecordMs float64       // press→mic-live, filled at record start; logged with the transcription metrics
}

var configMu sync.Mutex

// captureMu guards the live captureDevice, plus selectedDevice and
// preferredDevice, which the device-monitor goroutine and the tray device
// callback hot-swap on connect/disconnect while recordSessions reads the capture
// device for each new recording.
var captureMu sync.Mutex

var trayRecordChan = make(chan struct{}, 1)
var isRecording atomic.Bool
var accessibilityPoll atomic.Bool

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
	// Bare subcommands, parsed before the flag set (like git/go verbs). The
	// -setup flag below stays as an alias so install.sh and older docs keep
	// working.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "setup":
			os.Exit(setup.Run())
		case "doctor":
			os.Exit(setup.Doctor())
		case "update":
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
			fmt.Printf("Updating %s → %s...\n", version, rel.Version)
			app, err := update.Install(*rel)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			// The swap changed the (ad-hoc) code signature, so macOS dropped
			// the TCC grants — setup re-grants and re-verifies as the new
			// binary. Its exit code is the update's: an updated app that can't
			// hear is not a successful update.
			fmt.Println("Update installed. macOS resets permissions when the app changes — running setup to restore them.")
			code, err := setup.SpawnSetupAt(app)
			if err != nil {
				fmt.Printf("Zee %s is installed, but setup could not start: %v\n", rel.Version, err)
				fmt.Printf("Run it manually: %s/Contents/MacOS/zee setup\n", app)
				os.Exit(1)
			}
			os.Exit(code)
		}
	}

	benchmarkFile := flag.String("benchmark", "", "Run benchmark with WAV file instead of live recording")
	benchmarkRuns := flag.Int("runs", 3, "Number of benchmark iterations")
	autoPasteFlag := flag.Bool("autopaste", true, "Auto-paste to focused window after transcription")
	setupFlag := flag.Bool("setup", false, "Run the interactive setup wizard (provider, key, device, permissions, hotkey) and exit")
	deviceFlag := flag.String("device", "", "Use named microphone device")
	formatFlag := flag.String("format", "mp3@16", "Audio format: mp3@16, mp3@64, or flac")
	versionFlag := flag.Bool("version", false, "Print version and exit")
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
			// An unreadable credentials.json hides every cloud key, so the
			// generic "run zee setup" would send the user to reconfigure keys
			// that are already there. Name the actual problem.
			if cerr := config.CredentialsError(); cerr != nil {
				fatal("Credentials could not be read, so no provider is configured.\n\n%v\n\nFix %s and start Zee again.", cerr, config.CredentialsPath())
			}
			fatal("%v", initErr)
		}
	}
	streamEnabled = modelSupportsStream(activeTranscriber)
	if *langFlag != "" {
		activeTranscriber.SetLanguage(*langFlag)
	}

	log.SetTranscribeEnabled(*debugTranscribeFlag)
	if err := log.Init(); err != nil {
		alert.Warn("Diagnostic logging will not work.\n\n" + err.Error())
	} else {
		log.SessionStart(activeTranscriber.Name(), activeFormat, activeFormat)
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
		ensureAutoPasteAccessibility()
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
	// preferredDevice remembers the user's choice so we can auto-reconnect.
	// Seeded from the saved/flag name — NOT from what's currently attached —
	// so a session started while the mic is unplugged still auto-switches to
	// it the moment it's plugged in (capture starts on system default until
	// then).
	preferredDevice := *deviceFlag
	tray.SetBTCheck(audio.IsBluetooth)
	if devices, err := ctx.Devices(); err == nil && len(devices) > 0 {
		tray.SetDevices(deviceNames(devices), preferredDevice, func(name string) {
			if guardBusy("Can't change the microphone while recording or transcribing.") {
				return
			}
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
		from := activeTranscriber.Name() + "/" + activeTranscriber.GetModel()
		var outgoing transcriber.Transcriber
		if activeTranscriber.Name() != p.Name {
			outgoing = activeTranscriber
			newTr := p.New()
			newTr.SetLanguage(activeTranscriber.GetLanguage())
			activeTranscriber = newTr
		}
		activeTranscriber.SetModel(model) // local: kicks off a background gguf load, returns at once
		log.Info(fmt.Sprintf("model_switch from=%s to=%s/%s", from, p.Name, model))
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
			ct := time.Now()
			c.Close()
			log.Info(fmt.Sprintf("model_close freed=%s close_ms=%d", from, time.Since(ct).Milliseconds()))
		}

		config.Update(func(s *config.Settings) { s.Provider = p.Name; s.Model = model })
		tray.SetLanguages(langs)
		tray.SetHintsEnabled(!local)
		tray.SetActiveModel(p.Name, model)
	}

	// switchModel is the guarded wrapper for a user-initiated model/provider
	// change: it denies while a record/inference cycle is active (so neither the
	// gguf reload nor the Close of the outgoing model can free a ctx an in-flight
	// session is using), otherwise applies the swap. Only the tray menu calls it.
	switchModel := func(p transcriber.ProviderInfo, model string) {
		if guardBusy("Can't switch models while recording or transcribing.") {
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
				// Don't auto-switch: a download finishing shouldn't yank the active
				// model out from under an in-flight dictation. Notify and let the
				// user select it — the menu already shows it ready.
				label := model
				if m, ok := modelIndex[provider+":"+model]; ok {
					label = m.Label
				}
				alert.Info(label + " downloaded and ready.\n\nSelect it from the menu to use it.")
			}()
		}
	})

	tray.SetLanguage(*langFlag, func(code string, persist bool) {
		// Only a real user choice can be denied. Derived changes arrive with
		// persist=false — a model-constraint fallback, a config-file reload,
		// the language list being rebuilt — and must apply silently: they are
		// just a string assignment (the in-flight session already captured its
		// language), and denying one pops a modal at a user who did nothing.
		if persist && guardBusy("Can't change the language while recording or transcribing.") {
			return
		}
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
		// Materialize an unset hotkey as the active default so the opened file
		// shows a readable combo (⌃⇧Space) instead of "key": 0 / empty label.
		// config.Update also creates config.json if it's missing — open needs a file.
		config.Update(func(s *config.Settings) {
			if len(s.Hotkey.Mods) == 0 && s.Hotkey.Key == 0 {
				d := hotkey.DefaultCombo()
				s.Hotkey = config.Hotkey{Mods: d.Mods, Key: d.Key, Label: d.Label}
			}
		})
		exec.Command("open", "-t", config.SettingsPath()).Run()
	})
	tray.OnEditCredentials(func() {
		// Keys are read fresh on every APIKey() call, so an edit takes effect
		// on the next transcription — no watcher, no restart.
		if err := config.EnsureCredentials(); err != nil {
			alert.Warn("Could not create the credentials file.\n\n" + err.Error())
			return
		}
		exec.Command("open", "-t", config.CredentialsPath()).Run()
	})
	tray.SetHotkeyLabel(currentHotkeyCombo(cfg).Display())

	trayQuit := tray.Init()
	tray.OnAutoPaste(func(on bool) {
		configMu.Lock()
		autoPaste = on
		configMu.Unlock()
		config.Update(func(s *config.Settings) { s.AutoPaste = on })
		if on {
			go ensureAutoPasteAccessibility()
		}
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
			names := deviceNames(devices)
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
			switch deviceChangeAction(names, selName, pref) {
			case switchToDefault:
				log.Info("device_disconnected: " + selName)
				applyDeviceSwitch(ctx, captureConfig, &captureDevice, &selectedDevice, nil)
				selName = ""
			case switchToPreferred:
				log.Info("device_reconnected: " + pref)
				switchDeviceByName(ctx, captureConfig, &captureDevice, &selectedDevice, pref)
				selName = pref
			}
			tray.RefreshDevices(names, selName)
		}
	}()

	// Notify-only: the tray never installs. Updating swaps the bundle, which
	// resets the TCC grants (ad-hoc signature) and requires the interactive
	// setup re-grant — a terminal flow the running tray app can't host.
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
			cmd := "/Applications/Zee.app/Contents/MacOS/zee update"
			if exe, err := os.Executable(); err == nil {
				cmd = exe + " update"
			}
			alert.Info("Update available: " + version + " → " + rel.Version +
				"\n\nQuit Zee (menu bar → Quit), then run in a terminal:\n\n" + cmd +
				"\n\nThe update re-runs setup — macOS resets permissions when the app changes.")
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
		// A bad saved combo (unknown modifier, unregistrable chord) must not
		// brick every launch. If it wasn't the default, fall back to the
		// default and warn — the user can re-bind from Settings; only a failing
		// default is fatal (nothing left to try).
		def := hotkey.DefaultCombo()
		if currentHotkeyCombo(cfg).Equal(def) {
			fatal("Failed to register hotkey: %v\n\nGrant Accessibility access in System Settings → Privacy & Security.", err)
		}
		log.Warnf("falling back to default hotkey %s", def.Label)
		hk = hotkey.New(def)
		if err := hk.Register(); err != nil {
			fatal("Failed to register hotkey: %v\n\nGrant Accessibility access in System Settings → Privacy & Security.", err)
		}
		tray.SetHotkeyLabel(def.Display())
		tray.SetError("Saved hotkey couldn't be registered — using " + def.Display() + ". Re-bind it in Settings.")
	}
	defer hk.Unregister()

	sessions := make(chan recSession, 1)
	go listenHotkey(hk, longPressDuration(), sessions)

	// Live-reload external config.json edits (tray "Edit Settings…"), applying
	// each changed field through the same path its tray callback uses. A reload
	// landing mid record/inference cycle is deferred to cycle end, like model
	// switches. The watcher itself suppresses the app's own saves.
	applyCfg := func(s config.Settings) {
		configMu.Lock()
		curProv, curModel := activeTranscriber.Name(), activeTranscriber.GetModel()
		configMu.Unlock()
		// An empty model means "the provider's default" (its first listed model)
		// — what a hand-edit naturally omits. Resolving it here keeps that a
		// real switch, not a silent no-op. Models is a plain slice, so this is
		// cheap (no model load).
		reqProv, reqModel := s.Provider, s.Model
		if reqProv != "" && reqModel == "" {
			if p, ok := providerByName(reqProv); ok && len(p.Models) > 0 {
				reqModel = p.Models[0].ID
			} else {
				log.Warnf("settings reload: unknown provider %q, keeping %s/%s", reqProv, curProv, curModel)
			}
		}
		if reqProv != "" && reqModel != "" && (reqProv != curProv || reqModel != curModel) {
			// Only a ready model is applied — a file edit must not trigger a download.
			// applySwitch (raw), not switchModel: this reload path is internal and
			// already deferred to an idle moment by config.Watch → pendingReload, so
			// it must apply unconditionally rather than re-checking the busy guard.
			if p, ok := providerByName(reqProv); ok && p.Status(reqModel).Ready {
				applySwitch(p, reqModel)
			} else {
				log.Warnf("settings reload: %s/%s not available, keeping %s/%s", reqProv, reqModel, curProv, curModel)
			}
		}

		tray.SelectLanguage(s.Language)

		captureMu.Lock()
		devChanged := s.Device != preferredDevice
		preferredDevice = s.Device
		captureMu.Unlock()
		if devChanged {
			if s.Device == "" {
				applyDeviceSwitch(ctx, captureConfig, &captureDevice, &selectedDevice, nil)
			} else {
				switchDeviceByName(ctx, captureConfig, &captureDevice, &selectedDevice, s.Device)
			}
			if devices, err := ctx.Devices(); err == nil {
				names := deviceNames(devices)
				captureMu.Lock()
				sel := ""
				if selectedDevice != nil {
					sel = selectedDevice.Name
				}
				captureMu.Unlock()
				tray.RefreshDevices(names, sel)
			}
		}

		configMu.Lock()
		apChanged := autoPaste != s.AutoPaste
		autoPaste = s.AutoPaste
		configMu.Unlock()
		if apChanged {
			tray.SetAutoPaste(s.AutoPaste)
			if s.AutoPaste {
				go ensureAutoPasteAccessibility()
			}
		}

		if s.AutoStart != login.Enabled() {
			var err error
			if s.AutoStart {
				err = login.Enable()
			} else {
				err = login.Disable()
			}
			if err != nil {
				log.Errorf("settings reload: login toggle: %v", err)
			} else {
				tray.SetLogin(s.AutoStart)
			}
		}

		if want := currentHotkeyCombo(s); !want.Equal(hk.Current()) {
			if err := hk.Rebind(want); err != nil {
				log.Errorf("settings reload: hotkey %s: %v", want.Label, err)
				tray.SetError("Hotkey " + want.Display() + " rejected — keeping " + hk.Current().Display())
			} else {
				log.Info("settings reload: hotkey → " + want.Label)
				tray.SetHotkeyLabel(want.Display())
			}
		}
	}
	config.Watch(func(s config.Settings) {
		if isRecording.Load() {
			pendingMu.Lock()
			pendingReload = func() { applyCfg(s) }
			pendingMu.Unlock()
			return
		}
		applyCfg(s)
	})

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

// pendingReload holds a config-file reload deferred because the file changed
// mid-cycle; applyPendingReload runs it at cycle end. A file edit can't be
// denied (it already happened), so it defers — unlike user-initiated engine ops,
// which guardBusy denies outright. pendingMu guards it across goroutines.
var (
	pendingMu     sync.Mutex
	pendingReload func()
)

func applyPendingReload() {
	pendingMu.Lock()
	rl := pendingReload
	pendingReload = nil
	pendingMu.Unlock()
	if rl != nil {
		rl()
	}
}

// busyAlert ensures at most one "busy" dialog is open at a time: a second denial
// while it's showing only beeps, so rapid taps can't stack modal dialogs.
var busyAlert atomic.Bool

// guardBusy denies a user-initiated engine op while a record/inference cycle is
// active — the fragile window in which the model ctx must not be swapped or
// freed. It beeps immediately and pops one non-blocking dialog (the given
// warning) explaining why. Returns true when the op was denied. Every user
// action that touches the engine (record start, model/provider switch, device
// switch, language change) funnels through here; internal callers (startup
// restore, setup) use the raw functions and skip the guard.
func guardBusy(warning string) bool {
	if !isRecording.Load() {
		return false
	}
	// Log every denial. Without this a dialog that appears without the user
	// touching anything is untraceable: the alert names the action but nothing
	// records which caller fired it or what the engine state was.
	log.Warnf("denied (recording=true transcribing=%v): %s", isTranscribing.Load(), warning)
	audio.PlayDenied()
	if busyAlert.CompareAndSwap(false, true) {
		go func() {
			alert.Warn(warning)
			busyAlert.Store(false)
		}()
	}
	return true
}

// tryStartSession enqueues a fresh recording session unless a cycle is already
// active (recording OR still transcribing) — in which case guardBusy denies it.
// Returns the SilenceClose handle when it started a session, nil when it denied.
// The hotkey and the tray "Start Recording" button both funnel through here, so
// neither can queue an unattended recording that fires the instant inference ends.
func tryStartSession(sessions chan<- recSession) *atomic.Bool {
	if guardBusy("Already recording or transcribing.") {
		return nil
	}
	sc := &atomic.Bool{}
	audio.PlayStart() // reflexive: sound the press now, not after the record loop spins up (playOne is non-blocking)
	sessions <- recSession{Stop: resetStop(), SilenceClose: sc, PressedAt: time.Now()}
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
		applyPendingReload() // apply any config-file reload deferred during this cycle
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
	const def = hotkey.DefaultLongPress
	if v := os.Getenv("ZEE_LONGPRESS_DURATION"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Warnf("invalid ZEE_LONGPRESS_DURATION %q, using default %s", v, def)
	}
	return def
}

func listenHotkey(hk hotkey.Hotkey, longPress time.Duration, sessions chan<- recSession) {
	for {
		<-hk.Keydown()
		if isRecording.Load() {
			<-hk.Keyup()
			log.HotkeyPress(0, "denied")
			if isTranscribing.Load() {
				audio.PlayDenied() // ignored: transcription still in progress
			} else {
				requestStop()
			}
			continue
		}
		sc := tryStartSession(sessions)
		if sc == nil {
			log.HotkeyPress(0, "denied")
			<-hk.Keyup() // denied (a cycle began between the guard above and here)
			continue
		}
		// Shared press semantics (hold vs toggle) — see hotkey.WaitStop. Toggle
		// mode arms silence auto-close the moment it is entered.
		downToUp, toggled := hotkey.WaitStop(hk, longPress, func() { sc.Store(true) })
		mode := "hold"
		if toggled {
			mode = "toggle"
		}
		log.HotkeyPress(float64(downToUp.Milliseconds()), mode)
		requestStop()
	}
}

// deviceAction is what a change in the attached-device list means for the
// capture device.
type deviceAction int

const (
	keepDevice        deviceAction = iota
	switchToDefault                // the selected mic vanished
	switchToPreferred              // the user's preferred mic (re)appeared
)

// deviceChangeAction decides how the capture device should react to the
// current device list: switch away when the selected mic is gone, switch (back)
// to the preferred one when it is attached while we're on the system default —
// including the first time it appears after starting without it.
func deviceChangeAction(names []string, selected, preferred string) deviceAction {
	if selected != "" && !slices.Contains(names, selected) {
		return switchToDefault
	}
	if selected == "" && preferred != "" && slices.Contains(names, preferred) {
		return switchToPreferred
	}
	return keepDevice
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

// ensureAutoPasteAccessibility requests the one permission needed by
// auto-paste and watches for the asynchronous System Settings grant. It never
// changes the saved auto-paste preference: a grant makes the next recording
// paste normally, while a timeout leaves the preference intact for a later
// grant or app restart.
func ensureAutoPasteAccessibility() {
	if permissions.HasAccessibility() || !accessibilityPoll.CompareAndSwap(false, true) {
		return
	}
	alert.Warn("Auto-paste requires Accessibility permission.\n\nGrant access to Zee.app (or your terminal app if running from CLI) in:\nSystem Settings → Privacy & Security → Accessibility")
	permissions.RequestAccessibility()
	permissions.OpenAccessibilitySettings()

	go func() {
		defer accessibilityPoll.Store(false)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		timeout := time.NewTimer(time.Minute)
		defer timeout.Stop()
		for {
			select {
			case <-ticker.C:
				if permissions.HasAccessibility() {
					log.Info("Accessibility permission granted; auto-paste is ready")
					return
				}
			case <-timeout.C:
				log.Warn("Accessibility permission not granted; auto-paste remains unavailable")
				tray.SetError("Auto-paste is waiting for Accessibility permission")
				return
			}
		}
	}()
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
		tailWait:  time.Duration(config.Get().TailWaitMs) * time.Millisecond,
	}
	configMu.Unlock()
	if cfg.autoPaste && !permissions.HasAccessibility() {
		cfg.autoPaste = false
		tray.SetError("Auto-paste is waiting for Accessibility permission")
	}

	tSess, err := cfg.tr.NewSession(context.Background(), transcriber.SessionConfig{
		Stream:   cfg.stream,
		Format:   cfg.format,
		Language: cfg.lang,
		Hints:    cfg.hints,
	})
	if err != nil {
		return nil, err
	}

	// Save the clipboard before the first overwrite so it can be restored
	// after the paste — but never during the press: atotto's Read forks
	// pbpaste, and fork freezes every thread for O(resident memory) while the
	// kernel clones the page tables (~0.5s with a local model loaded), which
	// delays keyup delivery and misreads a quick tap as a hold. Saved lazily
	// instead: at the first streamed paste, or once recording has ended.
	clipCh := make(chan string, 1)
	var clipOnce sync.Once
	saveClip := func() { clipOnce.Do(func() { clipCh <- clip.SaveCurrent() }) }

	updatesDone := make(chan struct{})
	go func() {
		defer close(updatesDone)
		var prev string
		for text := range tSess.Updates() {
			if cfg.autoPaste && len(text) > len(prev) {
				saveClip()
				clip.PasteText(text[len(prev):])
			}
			prev = text
		}
	}()

	rec, err := newRecordingSession(capture, sess.Stop, tSess, sess.SilenceClose, cfg.tailWait)
	if err != nil {
		tSess.Close()
		return nil, err
	}
	if err := rec.Start(); err != nil {
		tSess.Close()
		return nil, err
	}
	// Reflex latency: press → mic live. Logged with the transcription metrics
	// (next to rss_mb) so a stall here — a fork or lock on the record-start path,
	// which scales with resident memory and makes quick taps misfire — is visible
	// in the field and correlatable with model size.
	if !sess.PressedAt.IsZero() {
		cfg.pressToRecordMs = float64(time.Since(sess.PressedAt).Milliseconds())
	}
	rec.Wait()

	if rec.totalFrames < uint64(encoder.SampleRate/10) {
		tSess.Close()
		return nil, nil
	}
	if cfg.autoPaste {
		go saveClip() // keys are up now; the pbpaste fork can't distort the press
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
		// Auto-save the failed recording so it can be recovered/retried, and
		// tell the user what actually happened — an error alert, not the
		// manual save's "Saved to" notice.
		if len(result.AudioData) > 0 {
			setLastRecording(result, cfg, closeErr.Error())
			// Persist synchronously — an immediate quit after the failure must
			// not lose the recording; only the dialog is fire-and-forget.
			msg := "Transcription failed:\n" + closeErr.Error()
			if cerr := config.CredentialsError(); cerr != nil {
				// No key was sent at all: the provider's "invalid api key" is a
				// symptom of the unreadable file, so lead with the real cause.
				msg = "Transcription failed — credentials could not be read, so no API key was sent.\n\n" +
					cerr.Error() + "\n\nFix it from the menu bar: Settings → Edit Credentials…\n\n" + closeErr.Error()
			}
			if dir, err := persistLastRecording(); err == nil {
				msg += "\n\nRecording saved to:\n" + dir
			}
			go alert.Error(msg)
		}
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
			PressToRecordMs:  cfg.pressToRecordMs,
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

	setLastRecording(result, cfg, "")
}

// setLastRecording stashes the just-finished recording (audio + metadata) so the
// tray "Save Last Recording" button and the auto-save-on-error path can persist
// it. errStr is the transcription error, if any ("" on success).
func setLastRecording(result transcriber.SessionResult, cfg recordingConfig, errStr string) {
	if len(result.AudioData) == 0 {
		return
	}
	lastRecMu.Lock()
	lastRec = &savedRecording{
		AudioData:   result.AudioData,
		AudioFormat: result.AudioFormat,
		Text:        result.Text,
		Provider:    cfg.tr.Name(),
		Model:       cfg.tr.GetModel(),
		Timestamp:   time.Now(),
		Err:         errStr,
	}
	lastRecMu.Unlock()
}

// saveLastRecording is the tray "Save Last Recording" button: persist + a
// success notice. The auto-save-on-error path uses persistLastRecording
// directly and shows its own error alert instead.
func saveLastRecording() {
	dir, err := persistLastRecording()
	if err != nil {
		alert.Warn(err.Error())
		return
	}
	alert.Info("Saved to " + dir)
}

// persistLastRecording writes the stashed last recording (audio + info.json)
// to a timestamped folder under samples/ and returns that folder.
func persistLastRecording() (string, error) {
	lastRecMu.Lock()
	rec := lastRec
	lastRecMu.Unlock()

	if rec == nil {
		return "", fmt.Errorf("no recording to save")
	}

	ts := rec.Timestamp.Format("2006-01-02T15-04-05")
	dir := filepath.Join(config.Dir(), "samples", ts)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("save failed: %w", err)
	}

	ext := rec.AudioFormat
	if err := os.WriteFile(filepath.Join(dir, "audio."+ext), rec.AudioData, 0644); err != nil {
		return "", fmt.Errorf("save failed: %w", err)
	}

	info, _ := json.Marshal(map[string]string{
		"provider":  rec.Provider,
		"model":     rec.Model,
		"format":    rec.AudioFormat,
		"text":      rec.Text,
		"error":     rec.Err,
		"timestamp": rec.Timestamp.Format(time.RFC3339),
	})
	os.WriteFile(filepath.Join(dir, "info.json"), info, 0644)

	return dir, nil
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
