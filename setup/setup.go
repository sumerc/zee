// Package setup is the interactive first-run wizard behind `zee -setup`. It
// configures everything needed for a working install — transcription provider +
// API key, input device, auto-paste, OS permissions (Microphone, Accessibility)
// and a custom push-to-talk hotkey — then verifies the result. It is idempotent:
// every step defaults to the current value, so a re-run on a configured machine
// is a few Enter presses, and a future `zee update` can re-run it to re-check
// permissions after replacing the binary.
//
// On macOS the wizard must run as the installed Zee.app (see maybeReexec) so TCC
// attributes the permission prompts to Zee rather than the terminal.
package setup

import (
	"fmt"
	"os"
	"time"

	"zee/audio"
	"zee/config"
	"zee/hotkey"
	"zee/internal/parakeet"
	"zee/localmodel"
	"zee/permissions"
	"zee/transcriber"
)

// Run executes the wizard and returns a process exit code (0 = mic granted, the
// one hard requirement; 1 otherwise).
func Run() int {
	if code, done := maybeReexec(); done {
		return code
	}

	transcriber.SetKeySource(config.APIKey)
	if err := config.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load settings: %v\n", err)
	}
	// A running tray instance would hold the global hotkey (breaking the capture
	// test) and shadow the post-setup launch. Quit it up front.
	quitRunningApp()

	fmt.Println("Zee setup")
	fmt.Println("=========")
	fmt.Println("Answer a few questions; press Enter to keep the current value.")

	chooseProvider()
	chooseDevice()
	chooseAutoPaste := autoPasteWanted()
	wantHotkey := hotkeyWanted()

	stepMic() // mandatory; verify() re-checks and sets the exit code

	axOK := true
	if chooseAutoPaste || wantHotkey {
		axOK = stepAccessibility()
	}
	if wantHotkey && axOK {
		stepHotkey()
	}

	code := verify()
	if config.Get().AutoPaste != chooseAutoPaste {
		config.Update(func(s *config.Settings) { s.AutoPaste = chooseAutoPaste })
	}

	fmt.Println()
	if code == 0 {
		fmt.Println("Setup complete.")
	} else {
		fmt.Println("Setup finished with warnings above — re-run `zee -setup` any time.")
	}
	launchInstalledApp()
	return code
}

// --- Provider + API key ---

func chooseProvider() {
	providers := transcriber.Providers()
	cur := config.Get().Provider

	labels := make([]string, len(providers))
	start := 0
	for i, p := range providers {
		label := p.Label
		switch {
		case p.Name == "parakeet":
			label += " — offline, no key"
		case config.HasAPIKey(p.Name):
			label += " — key set"
		}
		if p.Name == cur {
			label += "  [current]"
			start = i
		}
		labels[i] = label
	}

	idx := menu("Transcription provider:", labels, start)
	chosen := providers[idx]
	config.Update(func(s *config.Settings) { s.Provider = chosen.Name })

	if chosen.Name == "parakeet" {
		ensureLocalModel(chosen)
		return
	}
	promptAPIKey(chosen)
}

func promptAPIKey(p transcriber.ProviderInfo) {
	has := config.HasAPIKey(p.Name)
	prompt := fmt.Sprintf("%s API key: ", p.Label)
	if has {
		prompt = fmt.Sprintf("%s API key (Enter = keep existing): ", p.Label)
	}
	key := readSecret(prompt)
	if key == "" {
		if !has {
			fmt.Printf("  No key entered — %s stays unconfigured; add it later with `zee -setup`.\n", p.Label)
		}
		return
	}
	if err := config.SetAPIKey(p.Name, key); err != nil {
		fmt.Printf("  Could not save key: %v\n", err)
		return
	}
	fmt.Printf("  Saved %s key.\n", p.Label)
}

func ensureLocalModel(p transcriber.ProviderInfo) {
	if !parakeet.Available() {
		fmt.Println("  Note: the offline engine needs Apple Silicon; this machine will need a cloud provider.")
		return
	}
	modelID := config.Get().Model
	if modelID == "" {
		modelID = localmodel.ID110mEN
	}
	st := p.Status(modelID)
	if st.Ready {
		return
	}
	if !st.Downloadable {
		fmt.Printf("  Model %q is unavailable.\n", modelID)
		return
	}
	if !askYesNo(fmt.Sprintf("  Download the local model (%s)?", st.Detail), true) {
		fmt.Println("  Skipped — the app will offer it again on first use.")
		return
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
		return
	}
	fmt.Println("  ✓ model ready")
}

// --- Device ---

func chooseDevice() {
	cur := config.Get().Device
	label := "system default"
	if cur != "" {
		label = cur
	}
	if !askYesNo(fmt.Sprintf("\nChoose the input microphone? (current: %s)", label), false) {
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

// --- Auto-paste ---

func autoPasteWanted() bool {
	return askYesNo("\nAuto-paste transcribed text into the focused app?", config.Get().AutoPaste)
}

// --- Hotkey ---

func hotkeyWanted() bool {
	cur := currentCombo()
	return askYesNo(fmt.Sprintf("\nSet a custom push-to-talk hotkey? (current: %s)", cur.Label), false)
}

func stepHotkey() {
	fmt.Println("\nCustom hotkey")
	hk := hotkey.New(currentCombo())
	for {
		fmt.Println("  Press the modifier+key combo you want (Esc to keep the current one)…")
		c, err := hk.Capture(nil)
		if err != nil {
			fmt.Println("  Kept the current hotkey.")
			return
		}
		test := hotkey.New(c)
		if err := test.Register(); err != nil {
			fmt.Printf("  ✗ %s can't be used: %v\n", c.Label, err)
			if askYesNo("  Try another combo?", true) {
				continue
			}
			return
		}
		test.Unregister()
		config.Update(func(s *config.Settings) {
			s.Hotkey = config.Hotkey{Mods: c.Mods, Key: c.Key, Label: c.Label}
		})
		fmt.Printf("  ✓ hotkey set to %s\n", c.Label)
		return
	}
}

// --- Permissions ---

func stepMic() bool {
	fmt.Println("\nMicrophone permission (required)")
	switch permissions.MicrophoneStatus() {
	case permissions.MicGranted:
		fmt.Println("  ✓ already granted")
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

func stepAccessibility() bool {
	fmt.Println("\nAccessibility permission (for auto-paste and the global hotkey)")
	if permissions.HasAccessibility() {
		fmt.Println("  ✓ already granted")
		return true
	}
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

// --- Verify ---

func verify() int {
	fmt.Println("\nVerifying…")
	ok := true

	mic := permissions.MicrophoneStatus() == permissions.MicGranted
	report("microphone", mic, permissions.MicrophoneStatus().String())
	ok = ok && mic

	ax := permissions.HasAccessibility()
	report("accessibility", ax, boolWord(ax, "granted", "not granted"))

	combo := currentCombo()
	hkOK := hotkeyRegisters(combo)
	report("hotkey", hkOK, boolWord(hkOK, combo.Label, combo.Label+" (could not register)"))

	provOK, detail := providerReady()
	report("provider", provOK, detail)

	// Mic is the only hard requirement; the rest are warnings.
	if !mic {
		return 1
	}
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

func hotkeyRegisters(c hotkey.Combo) bool {
	hk := hotkey.New(c)
	if err := hk.Register(); err != nil {
		return false
	}
	hk.Unregister()
	return true
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
