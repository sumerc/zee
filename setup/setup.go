// Package setup is the interactive first-run wizard behind `zee -setup`. It
// configures everything needed for a working install and proves each piece
// works as it is configured — the microphone by recording and transcribing a
// real utterance with the local model, the push-to-talk hotkey by requiring a
// live fire, cloud providers by sending real audio with the stored key — so
// the final summary reports verified behavior, not just permission states.
// It is idempotent: every step defaults to the current value, so a re-run on a
// configured machine is a few Enter presses.
//
// On macOS the wizard must run as the installed Zee.app (see maybeReexec) so
// TCC attributes the permission prompts to Zee rather than the terminal.
package setup

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"zee/audio"
	"zee/config"
	"zee/encoder"
	"zee/hotkey"
	"zee/internal/parakeet"
	"zee/localmodel"
	"zee/permissions"
	"zee/transcriber"
)

// results collects what each step actually proved, for the final summary.
type results struct {
	micGranted bool
	micTested  bool
	testPCM    []byte // the mic-test recording, reused to test cloud providers
	combo      hotkey.Combo
	comboFired bool
	autoPaste  bool
	axGranted  bool
}

// Run executes the wizard and returns a process exit code (0 = mic granted,
// the one hard requirement; 1 otherwise).
func Run() int {
	if code, done := maybeReexec(); done {
		return code
	}
	// The launchd-parented wizard (re-exec'd, or launched by install.sh) must
	// not kill sibling zee processes: in the re-exec case its terminal parent
	// is a zee blocked on `open -W`, and in both cases the launcher already
	// quit the tray. Direct runs (dev binary, no-tty fallback) quit it here so
	// no tray instance holds the global hotkey during the tests below.
	if os.Getppid() != 1 {
		quitRunningApp()
	}

	transcriber.SetKeySource(config.APIKey)
	if err := config.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load settings: %v\n", err)
	}

	fmt.Println("Zee setup")
	fmt.Println("=========")
	fmt.Println("Answer a few questions; press Enter to keep the current value.")
	fmt.Println("Everything here can be changed later: re-run `zee -setup`, or edit")
	fmt.Println("config.json from the tray (Settings → Edit Settings…) — edits apply live.")

	var r results
	stepMic(&r)
	stepHotkey(&r)
	stepAutoPaste(&r)
	stepProviders(&r)
	code := summary(r)

	fmt.Println()
	if launchInstalledApp() {
		fmt.Println("Zee is running in your menu bar.")
	} else {
		fmt.Println("Start Zee normally (e.g. ./zee).")
	}
	return code
}

// --- Microphone (permission + device + live loopback test) ---

func stepMic(r *results) {
	fmt.Println("\nMicrophone (required)")
	r.micGranted = micPermission()
	chooseDevice()
	if !r.micGranted {
		return
	}
	if !parakeet.Available() {
		fmt.Println("  No offline engine on this machine — skipping the live mic test.")
		return
	}
	p, ok := providerByName("parakeet")
	if !ok || !ensureModel(p, localmodel.ID110mEN) {
		fmt.Println("  Skipping the live mic test (no local model).")
		return
	}
	for {
		pcm, err := record(4 * time.Second)
		if err != nil {
			fmt.Printf("  ✗ recording failed: %v\n", err)
			return
		}
		r.testPCM = pcm
		text, err := transcribeLocal(p, pcm)
		if err != nil {
			fmt.Printf("  ✗ transcription failed: %v\n", err)
			return
		}
		if text == "" {
			fmt.Println("  Heard nothing — is the right microphone selected?")
		} else {
			fmt.Printf("  Heard: %q\n", text)
			if askYesNo("  Is that roughly what you said?", true) {
				r.micTested = true
				fmt.Println("  ✓ microphone verified")
				return
			}
		}
		if !askYesNo("  Try again (you can pick another microphone)?", true) {
			return
		}
		chooseDevice()
	}
}

func micPermission() bool {
	switch permissions.MicrophoneStatus() {
	case permissions.MicGranted:
		fmt.Println("  ✓ permission already granted")
		return true
	case permissions.MicDenied:
		fmt.Println("  ✗ previously denied — enable Zee under System Settings → Privacy & Security → Microphone")
		return false
	}
	fmt.Println("  Approve the macOS prompt to allow recording…")
	if permissions.RequestMicrophone() == permissions.MicGranted {
		fmt.Println("  ✓ granted")
		return true
	}
	fmt.Println("  ✗ denied — enable it later under System Settings → Privacy & Security → Microphone")
	return false
}

func chooseDevice() {
	cur := config.Get().Device
	label := "system default"
	if cur != "" {
		label = cur
	}
	if !askYesNo(fmt.Sprintf("  Choose the input microphone? (current: %s)", label), false) {
		return
	}
	ctx, err := audio.NewContext()
	if err != nil {
		fmt.Printf("  Could not open audio: %v\n", err)
		return
	}
	defer ctx.Close()
	dev, err := audio.SelectDevice(ctx)
	if err != nil || dev == nil {
		fmt.Println("  Keeping the current device.")
		return
	}
	config.Update(func(s *config.Settings) { s.Device = dev.Name })
	fmt.Printf("  Using %s\n", dev.Name)
}

// record captures d of audio from the configured device (raw 16 kHz mono PCM).
func record(d time.Duration) ([]byte, error) {
	ctx, err := audio.NewContext()
	if err != nil {
		return nil, err
	}
	defer ctx.Close()

	var dev *audio.DeviceInfo
	if want := config.Get().Device; want != "" {
		if devices, err := ctx.Devices(); err == nil {
			for i := range devices {
				if devices[i].Name == want {
					dev = &devices[i]
					break
				}
			}
		}
	}

	cap, err := ctx.NewCapture(dev, audio.CaptureConfig{
		SampleRate: encoder.SampleRate,
		Channels:   encoder.Channels,
	})
	if err != nil {
		return nil, err
	}
	defer cap.Close()

	var mu sync.Mutex
	var buf []byte
	cap.SetCallback(func(data []byte, _ uint32) {
		mu.Lock()
		buf = append(buf, data...)
		mu.Unlock()
	})

	fmt.Printf("  Recording %.0fs — speak a short English sentence now", d.Seconds())
	if err := cap.Start(); err != nil {
		return nil, err
	}
	for end := time.Now().Add(d); time.Now().Before(end); {
		time.Sleep(500 * time.Millisecond)
		fmt.Print(".")
	}
	cap.Stop()
	fmt.Println(" done")

	mu.Lock()
	defer mu.Unlock()
	return buf, nil
}

// transcribeLocal runs pcm through the local English model (loaded for the
// test, freed right after — the gguf holds C memory the GC can't reclaim).
func transcribeLocal(p transcriber.ProviderInfo, pcm []byte) (string, error) {
	tr := p.New()
	tr.SetModel(localmodel.ID110mEN)
	defer func() {
		if c, ok := tr.(interface{ Close() }); ok {
			c.Close()
		}
	}()
	return transcribePCM(tr, pcm)
}

// transcribePCM pushes pcm through the provider's normal session path and
// returns the transcript — the same round-trip a real dictation makes.
func transcribePCM(tr transcriber.Transcriber, pcm []byte) (string, error) {
	sess, err := tr.NewSession(context.Background(), transcriber.SessionConfig{
		Format:   "flac",
		Language: "en",
	})
	if err != nil {
		return "", err
	}
	sess.Feed(pcm)
	res, err := sess.Close()
	if err != nil {
		return "", err
	}
	return res.Text, nil
}

// ensureModel makes sure the given local model is on disk, offering the
// download when it isn't (install.sh prefetches it, but a flaky network may
// have skipped it). Reports whether the model is ready.
func ensureModel(p transcriber.ProviderInfo, modelID string) bool {
	st := p.Status(modelID)
	if st.Ready {
		return true
	}
	if !st.Downloadable {
		fmt.Printf("  Model %q is unavailable.\n", modelID)
		return false
	}
	if !askYesNo(fmt.Sprintf("  Local model missing. Download it (%s)?", st.Detail), true) {
		fmt.Println("  Skipped — the app will offer it again on first use.")
		return false
	}
	fmt.Print("  Downloading")
	last := -1
	err := p.Download(modelID, func(f float64) {
		if pct := int(f * 100); pct/10 != last/10 {
			last = pct
			fmt.Printf(" %d%%", pct)
		}
	})
	fmt.Println()
	if err != nil {
		fmt.Printf("  Download failed: %v (retry later with `zee -setup`)\n", err)
		return false
	}
	fmt.Println("  ✓ model ready")
	return true
}

// --- Hotkey (capture optional, live fire test always) ---

func stepHotkey(r *results) {
	fmt.Println("\nPush-to-talk hotkey (required)")
	combo := currentCombo()
	if askYesNo(fmt.Sprintf("  Use a custom hotkey instead of %s?", combo.Label), false) {
		// Capturing keystrokes needs the Accessibility permission (NSEvent
		// global monitor); plain registration below does not.
		if ensureAccessibility("capturing a custom hotkey") {
			if c, ok := captureCombo(combo); ok {
				combo = c
			}
		}
	}
	for {
		if err := fireTest(combo); err == nil {
			r.comboFired = true
			break
		} else {
			fmt.Printf("  ✗ %s: %v\n", combo.Label, err)
		}
		if !askYesNo("  Try a different combo?", true) || !ensureAccessibility("capturing a custom hotkey") {
			break
		}
		c, ok := captureCombo(combo)
		if !ok {
			break
		}
		combo = c
	}
	r.combo = combo
	config.Update(func(s *config.Settings) {
		s.Hotkey = config.Hotkey{Mods: combo.Mods, Key: combo.Key, Label: combo.Label}
	})
	if r.comboFired {
		fmt.Printf("  ✓ hotkey %s saved\n", combo.Label)
	} else {
		fmt.Printf("  Saved %s unconfirmed — change it later by re-running `zee -setup`\n", combo.Label)
	}
}

// captureCombo records a chord from the user and proves it can register,
// looping until a usable one is captured or the user gives up.
func captureCombo(cur hotkey.Combo) (hotkey.Combo, bool) {
	hk := hotkey.New(cur)
	for {
		fmt.Println("  Press the modifier+key combo you want (Esc to keep the current one)…")
		c, err := hk.Capture(nil)
		if err != nil {
			fmt.Printf("  Kept %s.\n", cur.Label)
			return hotkey.Combo{}, false
		}
		test := hotkey.New(c)
		if err := test.Register(); err != nil {
			fmt.Printf("  ✗ %s can't be used: %v\n", c.Label, err)
			if askYesNo("  Try another combo?", true) {
				continue
			}
			return hotkey.Combo{}, false
		}
		test.Unregister()
		return c, true
	}
}

// fireTest registers c and waits for one real press. Registration alone can
// succeed for system-owned combos (⌘Space) that never deliver events — a live
// fire is the only proof the binding works.
func fireTest(c hotkey.Combo) error {
	hk := hotkey.New(c)
	if err := hk.Register(); err != nil {
		return err
	}
	defer hk.Unregister()
	fmt.Printf("  Press %s once to confirm it fires…\n", c.Label)
	select {
	case <-hk.Keydown():
		select {
		case <-hk.Keyup():
		case <-time.After(3 * time.Second):
		}
		fmt.Println("  ✓ fired")
		return nil
	case <-time.After(20 * time.Second):
		return fmt.Errorf("no event within 20s — the combo may be reserved by the system")
	}
}

// --- Auto-paste + Accessibility ---

func stepAutoPaste(r *results) {
	r.autoPaste = askYesNo("\nAuto-paste transcribed text into the focused app?", config.Get().AutoPaste)
	config.Update(func(s *config.Settings) { s.AutoPaste = r.autoPaste })
	if r.autoPaste {
		fmt.Println("  Auto-paste needs the Accessibility permission.")
		r.axGranted = ensureAccessibility("auto-paste")
	} else {
		r.axGranted = permissions.HasAccessibility()
		fmt.Println("  Enable it later any time from the tray → Settings → Auto-paste.")
	}
}

// ensureAccessibility prompts for the Accessibility permission and polls until
// macOS actually reports this binary as trusted (the grant is per-signature;
// the poll is the "is it really respected?" check), or times out.
func ensureAccessibility(reason string) bool {
	if permissions.HasAccessibility() {
		return true
	}
	fmt.Printf("  Accessibility permission is needed for %s.\n", reason)
	fmt.Println("  A prompt is opening — enable Zee under Privacy & Security → Accessibility, then return here.")
	permissions.RequestAccessibility()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if permissions.HasAccessibility() {
			fmt.Println("  ✓ granted")
			return true
		}
		time.Sleep(time.Second)
	}
	fmt.Println("  ✗ not granted (timed out) — enable it later and re-run `zee -setup`")
	return false
}

// --- Providers (configure any number, live-test each, pick the active one) ---

func stepProviders(r *results) {
	fmt.Println("\nTranscription providers")
	fmt.Println("  Configure as many as you like — each key is tested with real audio and")
	fmt.Println("  stored in credentials.json. Add or change keys later by re-running `zee -setup`.")
	providers := transcriber.Providers()
	for {
		cur := config.Get().Provider
		start := len(providers) // "Done"
		labels := make([]string, 0, len(providers)+1)
		for i, p := range providers {
			label := p.Label
			switch {
			case p.Name == "parakeet" && p.Available():
				label += " — offline, ready"
			case p.Name == "parakeet":
				label += " — offline, no key needed"
			case config.HasAPIKey(p.Name):
				label += " — key set ✓"
			}
			if p.Name == cur {
				label += "  [active]"
				start = i
			}
			labels = append(labels, label)
		}
		labels = append(labels, "Done")
		idx := menu("Configure a provider:", labels, start)
		if idx == len(labels)-1 {
			break
		}
		p := providers[idx]
		if p.Name == "parakeet" {
			if !parakeet.Available() {
				fmt.Println("  The offline engine needs Apple Silicon; use a cloud provider on this machine.")
				continue
			}
			ensureModel(p, defaultLocalModelID())
			continue
		}
		promptAPIKey(p)
		if config.HasAPIKey(p.Name) {
			testProvider(p, r.testPCM)
		}
	}
	chooseActiveProvider()
}

func promptAPIKey(p transcriber.ProviderInfo) {
	has := config.HasAPIKey(p.Name)
	prompt := fmt.Sprintf("  %s API key: ", p.Label)
	if has {
		prompt = fmt.Sprintf("  %s API key (Enter = keep existing): ", p.Label)
	}
	key := readSecret(prompt)
	if key == "" {
		if !has {
			fmt.Printf("  No key entered — %s stays unconfigured.\n", p.Label)
		}
		return
	}
	if err := config.SetAPIKey(p.Name, key); err != nil {
		fmt.Printf("  Could not save key: %v\n", err)
		return
	}
	fmt.Printf("  Saved %s key.\n", p.Label)
}

// testProvider sends real audio (the mic-test recording, or a second of
// silence) through the provider's normal session path — the only proof the
// stored key actually authenticates.
func testProvider(p transcriber.ProviderInfo, pcm []byte) {
	if len(pcm) == 0 {
		pcm = make([]byte, encoder.SampleRate*2) // 1s of silence still exercises auth
	}
	fmt.Printf("  Testing %s…", p.Label)
	text, err := transcribePCM(p.New(), pcm)
	switch {
	case err != nil:
		fmt.Printf(" ✗ %v\n", err)
	case text == "":
		fmt.Println(" ✓ authenticated (no speech in the sample)")
	default:
		fmt.Printf(" ✓ heard: %q\n", text)
	}
}

func chooseActiveProvider() {
	var avail []transcriber.ProviderInfo
	for _, p := range transcriber.Providers() {
		if p.Available() {
			avail = append(avail, p)
		}
	}
	if len(avail) == 0 {
		fmt.Println("  No provider configured — Zee can't transcribe until one is (re-run `zee -setup`).")
		return
	}
	cur := config.Get().Provider
	chosen := avail[0]
	if len(avail) > 1 {
		start := 0
		labels := make([]string, len(avail))
		for i, p := range avail {
			labels[i] = p.Label
			if p.Name == cur {
				start = i
			}
		}
		chosen = avail[menu("Active provider:", labels, start)]
	}
	if chosen.Name != cur {
		config.Update(func(s *config.Settings) {
			s.Provider = chosen.Name
			s.Model = "" // model IDs are provider-specific; fall back to the new provider's default
		})
	}
	fmt.Printf("  Active provider: %s\n", chosen.Label)
}

// defaultLocalModelID is the saved model when it's a local one, else the 110M
// English default.
func defaultLocalModelID() string {
	if id := config.Get().Model; id != "" && config.Get().Provider == "parakeet" {
		return id
	}
	return localmodel.ID110mEN
}

func providerByName(name string) (transcriber.ProviderInfo, bool) {
	for _, p := range transcriber.Providers() {
		if p.Name == name {
			return p, true
		}
	}
	return transcriber.ProviderInfo{}, false
}

// --- Summary ---

func summary(r results) int {
	fmt.Println("\nSummary")

	micDetail := permissions.MicrophoneStatus().String()
	if r.micGranted {
		micDetail = "granted (live test skipped)"
		if r.micTested {
			micDetail = "recorded + transcribed ✓"
		}
	}
	report("microphone", r.micGranted, micDetail)

	report("hotkey", r.comboFired, r.combo.Label+boolWord(r.comboFired, " (fired)", " (not confirmed)"))

	axOK := r.axGranted || !r.autoPaste
	report("accessibility", axOK, boolWord(r.axGranted, "granted", boolWord(r.autoPaste, "missing — auto-paste won't work", "not needed")))

	provOK, detail := providerReady()
	report("provider", provOK, detail)

	fmt.Println("\nChange any of this later: re-run `zee -setup`, or tray → Settings →")
	fmt.Println("Edit Settings… (config.json edits apply live).")

	// Mic is the only hard requirement; the rest are warnings.
	if !r.micGranted {
		fmt.Println("\nSetup finished with warnings above — re-run `zee -setup` any time.")
		return 1
	}
	fmt.Println("\nSetup complete.")
	return 0
}

// providerReady reports whether the configured provider is usable (key set, or
// local model present), checking Available() without constructing a transcriber
// (which would eagerly load the local model's C context). With no provider saved
// it reports the first available one, matching transcriber.New's auto-detect.
func providerReady() (bool, string) {
	want := config.Get().Provider
	for _, p := range transcriber.Providers() {
		if want != "" && p.Name != want {
			continue
		}
		if p.Available() {
			return true, p.Label
		}
		if want != "" {
			return false, p.Label + " (not configured)"
		}
	}
	return false, "none configured"
}

func report(name string, ok bool, detail string) {
	mark := "✗"
	if ok {
		mark = "✓"
	}
	fmt.Printf("  %s %-14s %s\n", mark, name, detail)
}

func boolWord(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

// currentCombo is the saved hotkey, or the built-in default when none is saved.
func currentCombo() hotkey.Combo {
	h := config.Get().Hotkey
	c := hotkey.Combo{Mods: h.Mods, Key: h.Key, Label: h.Label}
	if c.IsZero() {
		return hotkey.DefaultCombo()
	}
	return c
}
