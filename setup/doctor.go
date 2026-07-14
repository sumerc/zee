package setup

import (
	"fmt"
	"os"
	"time"

	"zee/config"
	"zee/encoder"
	"zee/hotkey"
	"zee/permissions"
	"zee/transcriber"
)

// Doctor is `zee doctor`: a zero-question health check proving the saved
// config works end to end with this binary. It asks nothing and writes
// nothing — it registers the configured hotkey, has the user hold it and
// speak (the exact push-to-talk flow), transcribes the recording with the
// configured provider, and reports every check with an exit code (0 = all
// healthy). Configuration problems are fixed with `zee setup`, not here.
func Doctor() int {
	// Same guard as setup.Run: a live tray holds the hotkey this test needs.
	if !isRespawnedChild() && otherZeeRunning() {
		fmt.Println("Zee is already running — quit it first (menu bar → Quit), then re-run `zee doctor`.")
		return 1
	}
	if code, done := maybeReexec(); done {
		return code
	}

	transcriber.SetKeySource(config.APIKey)
	if err := config.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load settings: %v\n", err)
	}

	printBanner()
	fmt.Println(bold("Doctor — checking that this binary works with the saved config."))

	micOK := permissions.MicrophoneStatus() == permissions.MicGranted
	axOK := permissions.HasAccessibility()
	autoPaste := config.Get().AutoPaste
	combo := currentCombo()
	provOK, provLabel := providerReady()

	// The one live test: hold the configured hotkey, speak, release —
	// hotkey, microphone, and provider proven in a single real dictation.
	fired, text, liveErr := hotkeyDictation(combo)

	// Auto-paste enabled → prove a synthesized paste actually lands.
	pasteOK := true
	if autoPaste {
		pasteOK = axOK && pasteTest()
	}

	fmt.Println("\nReport")
	report("microphone", micOK, permissions.MicrophoneStatus().String())
	report("accessibility", axOK || !autoPaste,
		boolWord(axOK, "granted", boolWord(autoPaste, "missing — auto-paste won't work", "not granted (not needed)")))
	report("hotkey", fired, comboDisplay(combo)+boolWord(fired, " (fired)", " (did not fire)"))
	report("provider", provOK, provLabel)
	if autoPaste {
		report("paste", pasteOK, boolWord(pasteOK, "verified", "did not arrive"))
	}
	switch {
	case liveErr != nil:
		report("dictation", false, liveErr.Error())
	case text == "":
		report("dictation", false, "no speech recognized")
	default:
		report("dictation", true, fmt.Sprintf("%q", text))
	}

	healthy := micOK && fired && provOK && liveErr == nil && text != "" && (axOK || !autoPaste) && pasteOK
	fmt.Println()
	if healthy {
		fmt.Println("All checks passed.")
	} else {
		fmt.Println("Problems above — fix them with `zee setup`.")
	}

	if launchInstalledApp() {
		fmt.Println("Zee is running in your menu bar.")
	}
	if healthy {
		return 0
	}
	return 1
}

// hotkeyDictation runs one real push-to-talk cycle: register the combo, wait
// for the user to hold it, record until release, transcribe with the
// configured provider.
func hotkeyDictation(combo hotkey.Combo) (fired bool, text string, err error) {
	hk := hotkey.New(combo)
	if err := hk.Register(); err != nil {
		return false, "", fmt.Errorf("hotkey register: %v", err)
	}
	defer hk.Unregister()

	fmt.Printf("\nHold %s and speak, release to stop (or tap to start / tap to stop)…\n", comboDisplay(combo))
	// The app's own press semantics, verbatim (hotkey.AwaitRecord/WaitStop):
	// doctor must exercise the code the tray runs, not a reimplementation.
	stop, fired := hotkey.AwaitRecord(hk, hotkey.DefaultLongPress, 30*time.Second)
	if !fired {
		return false, "", fmt.Errorf("hotkey did not fire within 30s")
	}
	fmt.Print("  ● Recording… ")
	pcm, err := captureUntil(stop, 30*time.Second)
	fmt.Println("done")
	if err != nil {
		return true, "", err
	}
	if len(pcm) < encoder.SampleRate/5 { // <100ms of audio
		return true, "", fmt.Errorf("recording too short")
	}

	p, ok := activeProvider()
	if !ok {
		return true, "", fmt.Errorf("no provider configured")
	}
	tr := p.New()
	if m := config.Get().Model; m != "" && p.Name == config.Get().Provider {
		tr.SetModel(m)
	}
	defer func() {
		if c, ok := tr.(interface{ Close() }); ok {
			c.Close()
		}
	}()
	fmt.Printf("  Transcribing with %s…\n", p.Label)
	text, err = transcribePCM(tr, pcm, config.Get().Language)
	return true, text, err
}

// activeProvider resolves the provider doctor should test: the configured one,
// or — matching transcriber.New's auto-detect — the first available.
func activeProvider() (transcriber.ProviderInfo, bool) {
	if name := config.Get().Provider; name != "" {
		return providerByName(name)
	}
	for _, p := range transcriber.Providers() {
		if p.Available() {
			return p, true
		}
	}
	return transcriber.ProviderInfo{}, false
}
