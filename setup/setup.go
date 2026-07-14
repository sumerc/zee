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
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"zee/audio"
	"zee/clipboard"
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
	for _, a := range os.Args[1:] {
		if a == "-no-banner" {
			return
		}
	}
	if term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Print("\x1b[37m" + banner + "\x1b[0m\n")
	} else {
		fmt.Print(banner + "\n")
	}
}

// bold wraps s in ANSI bold on a tty, plain otherwise.
func bold(s string) string { return sgr("1", s) }

// tick / cross are the pass/fail glyphs, green/red on a tty. ANSI SGR colors
// are safe in any modern terminal; non-ttys get the bare glyph.
func tick() string  { return sgr("32", "✓") }
func cross() string { return sgr("31", "✗") }

func sgr(code, s string) string {
	if term.IsTerminal(int(os.Stdout.Fd())) {
		return "\x1b[" + code + "m" + s + "\x1b[0m"
	}
	return s
}

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
	fmt.Println(bold("Everything here can be changed later: re-run `zee setup`, or edit"))
	fmt.Println(bold("config.json from the tray (Settings → Edit Settings…) — edits apply live."))

	var r results
	stepMic(&r)
	stepHotkey(&r)
	stepAutoPaste(&r)
	stepProviders(&r)
	code := summary(r)

	if launchInstalledApp() {
		fmt.Println("Zee is running in your menu bar.")
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
			fmt.Printf("  "+cross()+" recording failed: %v\n", err)
			return
		}
		r.testPCM = pcm
		text, err := transcribeLocal(p, pcm)
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
				fmt.Println("  "+tick()+" microphone verified")
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
		fmt.Println("  "+tick()+" permission already granted")
		return true
	case permissions.MicDenied:
		fmt.Println("  " + cross() + " previously denied — opening System Settings; enable Zee under Privacy & Security → Microphone")
		permissions.OpenMicrophoneSettings()
		return false
	}
	fmt.Println("  Approve the macOS prompt to allow recording…")
	if permissions.RequestMicrophone() == permissions.MicGranted {
		fmt.Println("  "+tick()+" granted")
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

	idx := menu("  Microphone:", labels, start)
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
	fmt.Println("  Speak a short English sentence…")
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

// transcribeLocal runs pcm through the local English model (loaded for the
// test, freed right after — the gguf holds C memory the GC can't reclaim).
func transcribeLocal(p transcriber.ProviderInfo, pcm []byte) (string, error) {
	tr := p.New()
	tr.SetModel(localmodel.ID110mEN)
	defer closeTranscriber(tr)
	return transcribePCM(tr, pcm, "en")
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
		fmt.Printf("  Download failed: %v (retry later with `zee setup`)\n", err)
		return false
	}
	fmt.Println("  "+tick()+" model ready")
	return true
}

// --- Hotkey (capture optional, live fire test always) ---

func stepHotkey(r *results) {
	fmt.Println("\nPush-to-talk hotkey (required)")
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
		fmt.Printf("  Saved %s unconfirmed — change it later by re-running `zee setup`\n", combo.Display())
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
	fmt.Printf("  Press %s once to confirm it fires…\n", c.Display())
	select {
	case <-hk.Keydown():
		select {
		case <-hk.Keyup():
		case <-time.After(3 * time.Second):
		}
		fmt.Println("  "+tick()+" fired")
		return nil
	case <-time.After(20 * time.Second):
		return fmt.Errorf("no event within 20s — the combo may be reserved by the system")
	}
}

// --- Auto-paste + Accessibility ---

// stepAutoPaste asks for auto-paste and, when wanted, makes it provably work
// before setup moves on: the Accessibility grant is required, and then a real
// synthesized paste must land (pasteTest). The user can back out — auto-paste
// is then turned off, because it cannot work without the permission.
func stepAutoPaste(r *results) {
	r.autoPaste = askYesNo("Will you use auto-paste? (types each transcription into the focused app)", config.Get().AutoPaste)
	if !r.autoPaste {
		r.axGranted = permissions.HasAccessibility()
		fmt.Println("  Enable it later any time from the tray → Settings → Auto-paste.")
		config.Update(func(s *config.Settings) { s.AutoPaste = false })
		return
	}
	fmt.Println("  Auto-paste needs the Accessibility permission (required).")
	for {
		if ensureAccessibility("auto-paste") && pasteTest() {
			r.axGranted = true
			config.Update(func(s *config.Settings) { s.AutoPaste = true })
			return
		}
		if askYesNo("  Auto-paste verification failed — try again?", true) {
			continue
		}
		fmt.Println("  Auto-paste disabled; grant Accessibility and re-enable it from the tray.")
		r.autoPaste = false
		config.Update(func(s *config.Settings) { s.AutoPaste = false })
		return
	}
}

// pasteTest proves a synthesized paste actually works — Accessibility being
// granted is not the same thing. It puts a token on the clipboard, sends the
// paste keystroke at the focused app (this terminal), and checks that the
// token arrives on our own stdin.
func pasteTest() bool {
	if err := clipboard.Init(); err != nil {
		fmt.Printf("  %s paste init: %v\n", cross(), err)
		return false
	}
	prev, _ := clipboard.Read()
	const token = "zee-paste-test"
	if err := clipboard.Copy(token + "\n"); err != nil {
		fmt.Printf("  %s clipboard write: %v\n", cross(), err)
		return false
	}
	fmt.Println("  Verifying paste — the test text should type itself below:")
	fmt.Print("  ")
	clipboard.Paste()
	got := readLineTimeout(5 * time.Second)
	clipboard.Copy(prev) // restore what the user had (even if empty — don't leak the token)
	if strings.Contains(got, token) {
		fmt.Println("  " + tick() + " paste works")
		return true
	}
	fmt.Println()
	fmt.Println("  " + cross() + " pasted text did not arrive")
	return false
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
	fmt.Println("  Opening System Settings — enable Zee under Privacy & Security → Accessibility, then return here.")
	permissions.RequestAccessibility()
	permissions.OpenAccessibilitySettings()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if permissions.HasAccessibility() {
			fmt.Println("  "+tick()+" granted")
			return true
		}
		time.Sleep(time.Second)
	}
	fmt.Println("  "+cross()+" not granted (timed out) — enable it later and re-run `zee setup`")
	return false
}

// --- Providers (configure any number, live-test each, pick the active one) ---

func stepProviders(r *results) {
	fmt.Println("\nTranscription providers")
	fmt.Println("  Configure as many as you like — each key is tested with real audio and")
	fmt.Println("  stored in credentials.json. Add or change keys later by re-running `zee setup`.")
	providers := transcriber.Providers()
	// tested marks providers proven with real audio this run: the tick in the
	// menu means "verified", not merely "configured". Parakeet earned its tick
	// in the mic step; every cloud key already on disk is verified right here,
	// before the menu shows, so the ticks reflect reality from the start.
	tested := map[string]bool{"parakeet": r.micTested}
	for _, p := range providers {
		if p.Name != "parakeet" && config.HasAPIKey(p.Name) {
			tested[p.Name] = testProvider(p, r.testPCM)
		}
	}
	for {
		cur := config.Get().Provider
		start := len(providers) // "Done"
		labels := make([]string, 0, len(providers)+1)
		for i, p := range providers {
			label := p.Label
			switch {
			case tested[p.Name] && p.Available():
				label += " " + tick() // verified this run; says it all
			case p.Name == "parakeet" && p.Available():
				label += " — offline, ready"
			case p.Name == "parakeet":
				label += " — offline, no key needed"
			case config.HasAPIKey(p.Name):
				label += " — key set"
			default:
				label += " — key not set"
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
		changed := promptAPIKey(p)
		// A key that already earned its tick this run and wasn't changed needs
		// no re-test — re-verifying the same key would just waste a round-trip.
		if config.HasAPIKey(p.Name) && (changed || !tested[p.Name]) {
			tested[p.Name] = testProvider(p, r.testPCM)
		}
	}
	chooseActiveProvider()
}

// promptAPIKey reports whether the stored key changed (Enter keeps the
// existing one and changes nothing).
func promptAPIKey(p transcriber.ProviderInfo) bool {
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
		return false
	}
	if err := config.SetAPIKey(p.Name, key); err != nil {
		fmt.Printf("  Could not save key: %v\n", err)
		return false
	}
	fmt.Printf("  Saved %s key.\n", p.Label)
	return true
}

// testProvider sends real audio (the mic-test recording, or a second of
// silence) through the provider's normal session path — the only proof the
// stored key actually authenticates. Always in English: the sample was
// recorded against the English-only local model, and every cloud provider
// supports English. Reports whether the key verified.
func testProvider(p transcriber.ProviderInfo, pcm []byte) bool {
	if len(pcm) == 0 {
		pcm = make([]byte, encoder.SampleRate*2) // 1s of silence still exercises auth
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

func chooseActiveProvider() {
	var avail []transcriber.ProviderInfo
	for _, p := range transcriber.Providers() {
		if p.Available() {
			avail = append(avail, p)
		}
	}
	if len(avail) == 0 {
		fmt.Println("  No provider configured — Zee can't transcribe until one is (re-run `zee setup`).")
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
			micDetail = "recorded + transcribed " + tick()
		}
	}
	report("microphone", r.micGranted, micDetail)

	report("hotkey", r.comboFired, r.combo.Display()+boolWord(r.comboFired, " (fired)", " (not confirmed)"))

	axOK := r.axGranted || !r.autoPaste
	report("accessibility", axOK, boolWord(r.axGranted, "granted", boolWord(r.autoPaste, "missing — auto-paste won't work", "not needed")))

	provOK, detail := providerReady()
	report("provider", provOK, detail)

	fmt.Println("\nChange any of this later: re-run `zee setup`, or tray → Settings →")
	fmt.Println("Edit Settings… (config.json edits apply live).")

	// Mic is the only hard requirement; the rest are warnings.
	if !r.micGranted {
		fmt.Println("\nSetup finished with warnings above — re-run `zee setup` any time.")
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
	mark := cross()
	if ok {
		mark = tick()
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
