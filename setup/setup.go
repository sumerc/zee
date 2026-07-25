// Package setup is the interactive first-run wizard behind `zee setup`, plus
// the `zee doctor` health check (doctor.go). It
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
	_ "embed"
	"fmt"
	"os"
	"os/signal"
	"slices"
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

	"golang.org/x/term"
)

// banner is ANSI-Shadow block letters; printed inset by one space, tinted
// white on a tty and plain otherwise.
const banner = `
 ███████╗███████╗███████╗
 ╚══███╔╝██╔════╝██╔════╝
   ███╔╝ █████╗  █████╗
  ███╔╝  ██╔══╝  ██╔══╝
 ███████╗███████╗███████╗
 ╚══════╝╚══════╝╚══════╝
`

// printBanner is skipped when -no-banner is among the args — install.sh prints
// the same banner itself before the model downloads, and the flag survives the
// maybeReexec hop (extras are forwarded).
func printBanner() {
	if slices.Contains(os.Args[1:], "-no-banner") {
		return
	}
	if term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Print("\x1b[37m" + banner + "\x1b[0m\n")
	} else {
		fmt.Print(banner + "\n")
	}
}

// bold / dim wrap s in ANSI styling on a tty, plain otherwise.
func bold(s string) string { return sgr("1", s) }
func dim(s string) string  { return sgr("2", s) }

// tick / cross / ring are the pass/fail/unverified glyphs (green/red/yellow on
// a tty). ANSI SGR colors are safe in any modern terminal; non-ttys get the
// bare glyph.
func tick() string  { return sgr("32", "✓") }
func cross() string { return sgr("31", "✗") }
func ring() string  { return sgr("33", "○") }

func sgr(code, s string) string {
	if colorEnabled() {
		return "\x1b[" + code + "m" + s + "\x1b[0m"
	}
	return s
}

// colorEnabled honors the NO_COLOR convention (no-color.org: any non-empty
// value disables color) on top of the tty check.
func colorEnabled() bool {
	return os.Getenv("NO_COLOR") == "" && term.IsTerminal(int(os.Stdout.Fd()))
}

// step prints a bold "Step n/4 · title" header so the user always knows where
// they are in the wizard.
func step(n int, title string) {
	fmt.Printf("\n%s\n", bold(fmt.Sprintf("Step %d/4 · %s", n, title)))
}

// results collects what each step actually proved, for the final summary.
type results struct {
	micGranted  bool
	micTested   bool
	micProvider string // local provider the mic test verified ("" if none)
	testPCM     []byte // the mic-test recording, reused to test cloud providers
	combo       hotkey.Combo
	comboFired  bool
	autoPaste   bool
	axGranted   bool
}

// begin is the shared Run/Doctor preamble: refuse to run alongside a live
// tray instance (it holds the global hotkey the tests need — the user quits
// it deliberately instead of us killing it; checked before the respawn so the
// message lands cleanly, and the disclaimed child skips it since the only
// other zee process is its own terminal parent), respawn TCC-disclaimed if
// needed, wire the credentials store, load settings, print the banner.
// done=true means return code immediately (guard refused or respawn ran).
func begin(cmd string) (code int, done bool) {
	if !isRespawnedChild() && otherZeeRunning() {
		fmt.Printf("Zee is already running — quit it first (menu bar → Quit), then re-run `%s`.\n", cmd)
		return 1, true
	}
	if code, done := maybeReexec(); done {
		return code, true
	}
	if config.IsAppBundle() {
		// install.sh (and users) often run setup from a TCC-protected folder
		// (~/Desktop/…); the wizard inherits that cwd, so any relative-path
		// touch by us or a child process (pbcopy, open) pops a "Zee wants to
		// access your Desktop folder" prompt — attributed to Zee because of the
		// disclaim respawn. Nothing here depends on cwd; move somewhere neutral.
		_ = os.Chdir(os.TempDir())
	}
	// Cooked-mode prompts (askYesNo, line reads) surface Ctrl+C as SIGINT; the
	// raw-mode readers handle byte 0x03 themselves. Either way interruption is
	// safe — every step persists as it completes — so say that and leave.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() { <-sig; exitInterrupted() }()
	transcriber.SetKeySource(config.APIKey)
	if err := config.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load settings: %v\n", err)
	}
	printBanner()
	return 0, false
}

// Run executes the wizard and returns a process exit code (0 = mic granted,
// the one hard requirement; 1 otherwise).
func Run() int {
	if code, done := begin("zee setup"); done {
		return code
	}
	fmt.Println()
	fmt.Println(bold("Everything here can be changed later: re-run `zee setup`, or edit"))
	fmt.Println(bold("config.json from the tray (Settings → Edit Settings…) — edits apply live."))
	fmt.Println(dim("Ctrl+C anytime — progress is saved."))

	var r results
	stepMic(&r)
	stepHotkey(&r)
	stepAutoPaste(&r)
	stepProviders(&r)
	code := summary(r)

	if launchInstalledApp() {
		fmt.Println("\n" + bold("Zee is running in your menu bar — enjoy!"))
		fmt.Printf("Hold %s in any app to dictate.\n", bold(currentCombo().Display()))
	}
	return code
}

// --- Microphone (permission + device + live loopback test) ---

func stepMic(r *results) {
	step(1, "Microphone (required)")
	// Kick the test engine load first, before any prompt: the model read and
	// first-run GPU pipeline compile run in the background while the user works
	// through the permission prompt, device choice, and the recording itself —
	// so the transcription after "Heard:" feels instant instead of stalling.
	// The engine is reused across retries — no reload per attempt.
	var (
		tr   transcriber.Transcriber
		prov transcriber.ProviderInfo
		lang string
	)
	if parakeet.Available() { // both offline engines share the darwin/arm64 gate
		if p, l, ok := testEngine(); ok {
			prov, lang = p, l
			tr = p.New()
			defer closeTranscriber(tr)
		}
	}
	r.micGranted = micPermission()
	chooseDevice()
	if !r.micGranted {
		return
	}
	if !parakeet.Available() {
		fmt.Println("  No offline engine on this machine — skipping the live mic test.")
		return
	}
	if tr == nil {
		fmt.Println("  Skipping the live mic test (no local model).")
		return
	}
	if lang == "" {
		fmt.Println("  Speak in any language — Whisper (auto-detect)")
	}
	for {
		pcm, err := record(4 * time.Second)
		if err != nil {
			fmt.Printf("  "+cross()+" recording failed: %v\n", err)
			return
		}
		r.testPCM = pcm
		settled := notifyIfSlow(1500*time.Millisecond,
			"  Still warming up the model (first run compiles GPU pipelines)...")
		text, err := transcribePCM(tr, pcm, lang)
		settled()
		if err != nil {
			fmt.Printf("  "+cross()+" transcription failed: %v\n", err)
			return
		}
		if text == "" {
			fmt.Println("  Heard nothing — is the right microphone selected?")
		} else {
			fmt.Printf("  Heard: %q\n", text)
			if askYesNo("  Is that roughly what you said?", true) {
				r.micTested = true
				r.micProvider = prov.Name
				fmt.Println("  " + tick() + " microphone verified")
				return
			}
		}
		if !askYesNo("  Try again? (No skips the mic check — you can pick another microphone on retry)", true) {
			return
		}
		chooseDevice()
	}
}

func micPermission() bool {
	switch permissions.MicrophoneStatus() {
	case permissions.MicGranted:
		fmt.Println("  " + tick() + " permission already granted")
		return true
	case permissions.MicDenied:
		fmt.Println("  " + cross() + " previously denied — opening System Settings; enable Zee under Privacy & Security → Microphone")
		permissions.OpenMicrophoneSettings()
		return false
	}
	fmt.Println("  Approve the macOS prompt to allow recording…")
	if permissions.RequestMicrophone() == permissions.MicGranted {
		fmt.Println("  " + tick() + " granted")
		return true
	}
	fmt.Println("  " + cross() + " denied — opening System Settings; enable Zee under Privacy & Security → Microphone")
	permissions.OpenMicrophoneSettings()
	return false
}

// chooseDevice lists every input device (plus System Default) with the active
// one marked; Enter on the highlighted active entry keeps it, picking another
// switches.
func chooseDevice() {
	ctx, err := audio.NewContext()
	if err != nil {
		fmt.Printf("  Could not open audio: %v\n", err)
		return
	}
	defer ctx.Close()
	devices, err := ctx.Devices()
	if err != nil {
		fmt.Printf("  Could not list devices: %v\n", err)
		return
	}

	cur := config.Get().Device
	start := 0
	labels := make([]string, 0, len(devices)+1)
	def := "System default"
	if cur == "" {
		def += "  [active]"
	}
	labels = append(labels, def)
	for i := range devices {
		l := devices[i].Name
		if devices[i].Name == cur {
			l += "  [active]"
			start = i + 1
		}
		labels = append(labels, l)
	}

	idx := selectIndex("Microphone", labels, start)
	name := ""
	if idx > 0 {
		name = devices[idx-1].Name
	}
	if name != cur {
		config.Update(func(s *config.Settings) { s.Device = name })
	}
	if name == "" {
		name = "system default"
	}
	fmt.Printf("  Using %s\n", name)
}

// record captures d of audio from the configured device (raw 16 kHz mono PCM),
// showing a countdown while it runs.
func record(d time.Duration) ([]byte, error) {
	fmt.Println("\n  Speak a short English sentence…")
	stop := make(chan struct{})
	go func() {
		defer close(stop)
		for remain := int(d.Seconds()); remain > 0; remain-- {
			fmt.Printf("\r  ● Recording… %ds ", remain)
			time.Sleep(time.Second)
		}
	}()
	pcm, err := captureUntil(stop, d+2*time.Second)
	fmt.Print("\r  ● Recording… done\n")
	return pcm, err
}

// captureUntil records PCM from the configured device until stop closes (or
// max elapses — the safety cap). Shared by the wizard's timed mic test and
// doctor's hotkey-driven dictation.
func captureUntil(stop <-chan struct{}, max time.Duration) ([]byte, error) {
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
	if err := cap.Start(); err != nil {
		return nil, err
	}
	select {
	case <-stop:
	case <-time.After(max):
	}
	cap.Stop()

	mu.Lock()
	defer mu.Unlock()
	return buf, nil
}

// closeTranscriber frees a transcriber's backing resources when it has any
// (local models hold C memory the GC can't reclaim).
func closeTranscriber(tr transcriber.Transcriber) {
	if c, ok := tr.(interface{ Close() }); ok {
		c.Close()
	}
}

// testEngine picks the engine for the live mic test: Whisper with auto-detect
// (speak any language, and the one-time GPU warm-up is paid here in setup, not
// on the first real dictation), falling back to the English Parakeet when the
// whisper model is missing and the user declines the download. The returned
// lang is "" for auto-detect, "en" for the fallback.
func testEngine() (transcriber.ProviderInfo, string, bool) {
	if p, ok := providerByName("whisper"); ok && ensureModel(p, localmodel.IDWhisperQ5) {
		return p, "", true
	}
	if p, ok := providerByName("parakeet"); ok && ensureModel(p, localmodel.ID110mEN) {
		return p, "en", true
	}
	return transcriber.ProviderInfo{}, "", false
}

// notifyIfSlow prints msg once if the returned func isn't called within d, so a
// first-run model load (the GPU pipeline compile) reads as "warming up" rather
// than a hung transcription. Deliberately time-based: asking the engine whether
// it is ready would mean a readiness signal on the Transcriber interface for
// one cosmetic line. A warm transcribe of the 4 s test clip is well under d.
func notifyIfSlow(d time.Duration, msg string) (settled func()) {
	stop := make(chan struct{})
	go func() {
		select {
		case <-stop:
		case <-time.After(d):
			fmt.Println(msg)
		}
	}()
	return func() { close(stop) }
}

// transcribePCM pushes pcm through the provider's normal session path and
// returns the transcript — the same round-trip a real dictation makes.
func transcribePCM(tr transcriber.Transcriber, pcm []byte, lang string) (string, error) {
	sess, err := tr.NewSession(context.Background(), transcriber.SessionConfig{
		Format:   "flac",
		Language: lang,
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
		fmt.Printf("  Download failed: %v (re-run `zee setup` to retry)\n", err)
		return false
	}
	fmt.Println("  " + tick() + " model ready")
	return true
}

// --- Hotkey (capture optional, live fire test always) ---

func stepHotkey(r *results) {
	step(2, "Push-to-talk hotkey (required)")
	combo := currentCombo()
	if askYesNo(fmt.Sprintf("  Use a custom hotkey instead of %s?", combo.Display()), false) {
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
			fmt.Printf("  "+cross()+" %s: %v\n", combo.Display(), err)
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
		fmt.Printf("  "+tick()+" hotkey %s saved\n", combo.Display())
	} else {
		fmt.Printf("  Saved %s unconfirmed — re-run `zee setup` any time to change it\n", combo.Display())
	}
}

// captureCombo records a chord from the user and proves it can register,
// looping until a usable one is captured or the user gives up.
func captureCombo(cur hotkey.Combo) (hotkey.Combo, bool) {
	hk := hotkey.New(cur)
	for {
		fmt.Println("  Press the modifier + key combo you want (Esc to keep the current one)…")
		c, err := hk.Capture(nil)
		if err != nil {
			fmt.Printf("  Kept %s.\n", cur.Display())
			return hotkey.Combo{}, false
		}
		test := hotkey.New(c)
		if err := test.Register(); err != nil {
			fmt.Printf("  "+cross()+" %s can't be used: %v\n", c.Display(), err)
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
	fmt.Printf("  Press %s once to confirm it fires… %s\n", c.Display(), dim("(waiting up to 20s)"))
	select {
	case <-hk.Keydown():
		select {
		case <-hk.Keyup():
		case <-time.After(3 * time.Second):
		}
		fmt.Println("  " + tick() + " fired")
		return nil
	case <-time.After(20 * time.Second):
		return fmt.Errorf("no event within 20s — the combo may be reserved by the system")
	}
}

// --- Auto-paste + Accessibility ---

// stepAutoPaste asks for auto-paste and, when wanted, requires the
// Accessibility grant that makes it work. Accessibility (AXIsProcessTrusted) is
// the whole contract: it's the permission macOS checks before CGEventPost — the
// synthesized Cmd+V — is delivered to another app, and it's keyed to this
// binary's signature (so a stale post-update entry reads as not-granted). We
// don't fire a test keystroke: at real dictation time the paste lands in
// whatever app is focused, never the terminal, so a terminal round-trip would
// only test an artificial scenario (and race terminal focus). Trust the grant.
func stepAutoPaste(r *results) {
	step(3, "Auto-paste")
	r.autoPaste = askYesNo("Will you use auto-paste? (types each transcription into the focused app)", config.Get().AutoPaste)
	if !r.autoPaste {
		r.axGranted = permissions.HasAccessibility()
		fmt.Println("  Enable it later any time from the tray → Settings → Auto-paste.")
		config.Update(func(s *config.Settings) { s.AutoPaste = false })
		return
	}
	if ensureAccessibility("auto-paste") {
		r.axGranted = true
		fmt.Println("  " + tick() + " auto-paste enabled")
		config.Update(func(s *config.Settings) { s.AutoPaste = true })
		return
	}
	fmt.Println("  Auto-paste disabled; grant Accessibility and re-enable it from the tray.")
	r.autoPaste = false
	config.Update(func(s *config.Settings) { s.AutoPaste = false })
}

// ensureAccessibility prompts for the Accessibility permission, opens the
// System Settings pane at the right page, and polls until macOS actually
// reports this binary as trusted (the grant is per-signature; the poll is the
// "is it really respected?" check), or times out.
func ensureAccessibility(reason string) bool {
	if permissions.HasAccessibility() {
		return true
	}
	fmt.Printf("  Accessibility permission is needed for %s.\n", reason)
	fmt.Println("  Opening System Settings → Privacy & Security → Accessibility:")
	fmt.Println("    • if Zee is NOT listed, enable it.")
	// After an update the old entry keeps the previous build's signature, so it
	// shows checked yet AXIsProcessTrusted stays false — toggling won't help,
	// only removing and re-adding rebinds the grant to the new binary.
	fmt.Println("    • if Zee IS already listed, remove it with the “−” button and add it back (an update changed its signature).")
	fmt.Println("  Then return here.")
	permissions.RequestAccessibility()
	permissions.OpenAccessibilitySettings()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if permissions.HasAccessibility() {
			fmt.Println("  " + tick() + " granted")
			return true
		}
		time.Sleep(time.Second)
	}
	fmt.Println("  " + cross() + " not granted (timed out) — enable it later and re-run `zee setup`")
	return false
}

// --- Providers (configure any number, live-test each, pick the active one) ---

func stepProviders(r *results) {
	step(4, "Transcription providers")
	fmt.Println("  Configure as many as you like — each key is tested with real audio and")
	fmt.Println("  stored in credentials.json. Change a key later from the tray:")
	fmt.Println("  Settings → Edit Credentials… (re-run `zee setup` to add a tested provider).")
	providers := transcriber.Providers()
	// tested marks providers proven with real audio this run: the tick in the
	// menu means "verified", not merely "configured". The local engine used in
	// the mic step earned its tick there; every cloud key already on disk is
	// verified right here, before the menu shows, so the ticks reflect reality
	// from the start.
	tested := map[string]bool{}
	if r.micProvider != "" {
		tested[r.micProvider] = r.micTested
	}
	for _, p := range providers {
		if !p.Local && config.HasAPIKey(p.Name) {
			tested[p.Name] = testProvider(p, r.testPCM)
		}
	}
	for {
		cur := config.Get().Provider
		labels := make([]string, 0, len(providers)+2)
		for _, p := range providers {
			label := p.Label
			switch {
			case tested[p.Name] && p.Available():
				label += " " + tick() // verified this run; says it all
			case p.Local && p.Available():
				label += " — offline, ready"
			case p.Local:
				label += " — offline, no key needed"
			case config.HasAPIKey(p.Name):
				label += " — key set"
			default:
				label += " — key not set"
			}
			if p.Name == cur {
				label += "  [active]"
			}
			labels = append(labels, label)
		}
		// "Done" is the pre-highlighted default, so the whole step is a single
		// Enter (or Esc) for anyone happy with the offline engine.
		labels = append(labels, "Done")
		idx := selectIndex("Configure a provider", labels, len(labels)-1)
		if idx >= len(providers) {
			break
		}
		p := providers[idx]
		if p.Local {
			if !parakeet.Available() { // one flag covers both offline engines
				fmt.Println("  The offline engines need Apple Silicon; use a cloud provider on this machine.")
				continue
			}
			ensureModel(p, localDefaultModel(p))
			continue
		}
		changed, backedOut := promptAPIKey(p)
		if backedOut {
			continue
		}
		// A key that already earned its tick this run and wasn't changed needs
		// no re-test — re-verifying the same key would just waste a round-trip.
		if config.HasAPIKey(p.Name) && (changed || !tested[p.Name]) {
			tested[p.Name] = testProvider(p, r.testPCM)
		}
	}
	// No "pick the active one" question: local is the default engine
	// (Providers() puts it first) and the tray switches providers any time.
	if ok, label := providerReady(); ok {
		fmt.Printf("  Active provider: %s — switch any time from the tray menu.\n", label)
	} else {
		fmt.Println("  No provider configured — Zee can't transcribe until one is (re-run `zee setup`).")
	}
}

// promptAPIKey reports whether the stored key changed (Enter keeps the
// existing one and changes nothing) and whether the user backed out with Esc
// (which must skip the key test entirely — backing out of a mis-click must
// not test whatever key happens to be stored).
func promptAPIKey(p transcriber.ProviderInfo) (changed, backedOut bool) {
	has := config.HasAPIKey(p.Name)
	desc := ""
	if has {
		desc = "Enter = keep existing"
	}
	key, ok := secretInput(p.Label+" API key", desc)
	if !ok {
		return false, true
	}
	if key == "" {
		if !has {
			fmt.Printf("  No key entered — %s stays unconfigured.\n", p.Label)
		}
		return false, false
	}
	if err := config.SetAPIKey(p.Name, key); err != nil {
		fmt.Printf("  Could not save key: %v\n", err)
		return false, false
	}
	fmt.Printf("  Saved %s key.\n", p.Label)
	return true, false
}

// sampleSpeechWAV is a ~1.6s English clip (copied from test/data/en.wav),
// embedded so a provider test has real speech to transcribe even when the user
// skipped the mic recording. Silence made Whisper-class providers hallucinate
// phantom text ("."), which read as a real (wrong) transcription.
//
//go:embed sample_en.wav
var sampleSpeechWAV []byte

// sampleSpeechPCM is the decoded clip (raw 16 kHz mono PCM, matching the WAV's
// format), computed once. nil only if the embed/decoding somehow fails.
var sampleSpeechPCM, _ = audio.WAVToPCM(sampleSpeechWAV)

// testProvider sends real audio through the provider's normal session path —
// the only proof the stored key actually authenticates. It prefers the user's
// mic-test recording; absent that, the embedded English sample; only if both
// are missing does it fall back to silence. Always English: every cloud
// provider supports it, and the sample is English. Reports whether it verified.
func testProvider(p transcriber.ProviderInfo, pcm []byte) bool {
	if len(pcm) == 0 {
		pcm = sampleSpeechPCM
	}
	if len(pcm) == 0 {
		pcm = make([]byte, encoder.SampleRate*2) // last-resort: silence still exercises auth
	}
	fmt.Printf("  Testing %s…", p.Label)
	text, err := transcribePCM(p.New(), pcm, "en")
	switch {
	case err != nil:
		fmt.Printf(" %s %v\n", cross(), err)
		return false
	case text == "":
		fmt.Println(" " + tick() + " authenticated (no speech in the sample)")
	default:
		fmt.Printf(" %s heard: %q\n", tick(), text)
	}
	return true
}

// defaultLocalModelID is the saved model when it's a local one, else the 110M
// English default.
// localDefaultModel is the model the wizard ensures for a local provider: the
// user's persisted choice when it belongs to this provider, else the
// provider's own default.
func localDefaultModel(p transcriber.ProviderInfo) string {
	if id := config.Get().Model; id != "" && config.Get().Provider == p.Name {
		return id
	}
	return p.DefaultModel
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

	switch {
	case r.micTested:
		report("microphone", true, "recorded + transcribed")
	case r.micGranted:
		reportWarn("microphone", "granted (live test skipped)")
	default:
		report("microphone", false, permissions.MicrophoneStatus().String())
	}

	if r.comboFired {
		report("hotkey", true, r.combo.Display()+" (fired)")
	} else {
		reportWarn("hotkey", r.combo.Display()+" (not confirmed)")
	}

	axOK := r.axGranted || !r.autoPaste
	report("accessibility", axOK, boolWord(r.axGranted, "granted", boolWord(r.autoPaste, "missing — auto-paste won't work", "not needed")))

	provOK, detail := providerReady()
	report("provider", provOK, detail)

	fmt.Println("\nChange any of this later: re-run `zee setup`, or tray → Settings →")
	fmt.Println("Edit Settings… (config.json edits apply live) / Edit Credentials… (API keys).")

	// Mic is the only hard requirement; the rest are warnings.
	if !r.micGranted {
		fmt.Println("\nSetup finished with warnings above — re-run `zee setup` any time.")
		return 1
	}
	fmt.Printf("\n%s %s\n", tick(), bold("Zee setup complete."))
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
	mark := cross()
	if ok {
		mark = tick()
	}
	fmt.Printf("  %s %-14s %s\n", mark, name, bold(detail))
}

// reportWarn is the middle state: configured but unverified/skipped — the
// wizard proved nothing either way, which neither ✓ nor ✗ says honestly.
func reportWarn(name, detail string) {
	fmt.Printf("  %s %-14s %s\n", ring(), name, bold(detail))
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
