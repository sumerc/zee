# Changelog

## Unreleased

- New `make bench-local` benchmarks local Parakeet inference in isolation (model load + `Transcribe`, no capture/encoder/network), complementing the end-to-end `make benchmark`. Every clip runs against every model present on disk, reporting `ns/op` plus a realtime factor (`xRT`); `WAV=` accepts a file or a directory, so the saved-samples folder can be used as a benchmark corpus of real recordings. Missing models and non-16 kHz-mono clips skip instead of failing. `make bench-save` appends a run to `benchmark.txt` as a labelled baseline block (machine, corpus, commit, model version, date) so results from several machines accumulate in one file
- The S16LE→float32 conversion the local engine needs is now `audio.PCMToF32`, alongside the existing `WAVToPCM`/`PCMToWAV`, instead of an inline loop in the Parakeet session
- The mic now stays open ~200ms after the hotkey is released so a fast keyup no longer clips the last word (the recording ended mid-speech, and the transcriber then guessed at the truncated word). This tail-wait previously ran only for streaming providers; it now applies to every provider, including the local Parakeet models. Configurable via `tail_wait_ms` in `config.json` (default 200; 0 disables it)
- Local Parakeet models now load in the background instead of blocking: startup no longer stalls while the default gguf loads, and switching models from the tray returns immediately rather than freezing the UI for the multi-second reload. The first dictation after launch or a switch waits on the load only if it hasn't finished — and since recording overlaps the load, that wait is usually zero. Metadata (current model, language) stays responsive during the load via a dedicated load lock, so nothing else blocks on the gguf read
- Engine-mutating actions are now denied while recording or transcribing instead of running mid-cycle: model/provider switch, microphone switch, language change, and starting another recording all bounce with the denied beep and a one-time dialog ("try again in a moment") if a record→inference cycle is active. This closes a window where switching or freeing a model could race an in-flight transcription. Rapid taps don't stack dialogs — only the first shows one. Startup and setup are unaffected (they run when idle)
- Finishing a model download no longer auto-switches to it: it shows a "downloaded and ready — select it from the menu" notice instead, so a long background download can't yank the active model out from under a dictation in progress
- Tray: a separator now sits above **Settings**, visually separating it from the recording/copy actions above it
- New tray item **Settings → Edit Credentials…** opens `credentials.json` in an editor, so changing a provider's API key no longer means quitting the app and walking the whole `zee setup` wizard. Keys are read fresh on every call, so an edit applies to the next recording with no restart; the file is created (0600, `{}`) if missing. Setup remains the way to *add* a provider, since it live-tests the key
- A malformed `credentials.json` now says so instead of masquerading as a bad key: it silently yields no keys at all, so the provider previously answered "invalid api key" while the key on disk was fine. `config.CredentialsError()` reports the parse failure, and both the transcription-failure alert and the startup "no provider" fatal lead with the real cause and point at the file

- Push-to-talk starts ~200–600ms faster on macOS: the mic device is created once and kept warm across recordings instead of a full CoreAudio teardown+reinit on every press. The per-press reinit stalled the main run loop, delaying hotkey keyup delivery — the cause of quick taps being misread as hold-and-release, worse with large local models loaded. Diagnostics from the era it was added show it never fixed the post-sleep staleness it targeted (the real culprit was a vanished USB mic's stale device ID, since handled by the device watcher). If starting the warm device fails, it is rebuilt once and retried
- macOS feedback tones now play via AVAudioPlayer instead of an app-managed malgo playback device: each beep is fire-and-forget, so the end-beep sounds instantly at recording stop (previously 100–600ms late behind a per-tone CoreAudio device rebuild that also stalled the run loop and contended with record start). Tones are still synthesized in code and handed to the OS as in-memory WAV bytes at startup — no files, no shared audio device, so playback no longer touches the capture device lock
- Fix quick taps misfiring as hold-and-release right after model switches (and intermittently otherwise): with auto-paste on, every press forked `pbpaste` to snapshot the clipboard — fork freezes all threads for O(resident memory) (~0.5s right after a model load/unload churned the page tables), delaying keyup past the tap/hold threshold. The snapshot now happens after recording ends (or at the first streamed paste), never in the press window

- Local Parakeet now runs on the Metal (GPU) backend: bumped parakeet.cpp to v0.4.0, which fixes the macOS Metal crash (upstream #2/PR #4 — GPU→CPU scheduler fallback plus a native depthwise-conv kernel). Same ggml pin (v0.13.0). Steady-state ~1.7x faster on the 110m English model and ~2.6x on the 0.6b multilingual model; the win matters mainly for the multilingual model. The GPU pipelines are pre-compiled at model load (a warmup transcribe) so the first dictation isn't stalled, and ggml's verbose per-run backend logging is silenced. Metal shaders are embedded, so the binary stays self-contained

- Setup wizard readability pass: bold "Step n/4 · …" headers, a dim "Ctrl+C anytime — progress is saved" note up front, the saved hotkey shown as "Hold ⌃⇧Space in any app to dictate" at the end, a "(waiting up to 20s)" hint on the hotkey fire test, bold summary values and final status lines, and a third summary state — yellow ○ for configured-but-unverified (skipped mic test, unconfirmed hotkey) instead of overstating with ✓. ANSI styling now honors NO_COLOR

- `zee update` now hands off to `zee setup` after swapping the bundle instead of silently relaunching: the app is ad-hoc signed, so every update changes the code signature and macOS drops the Microphone/Accessibility grants — the old flow relaunched into an app that could no longer hear, with no signal why. Setup (spawned TCC-disclaimed as the new binary, reusing the wizard's respawn machinery) re-grants and re-verifies, and its exit code is the update's. The tray's "Check for Updates" is notify-only now — it shows the terminal command instead of installing under the running app. The auto-relaunch machinery (`-wait-for-pid`, `WaitForPID`, launch-failure rollback) is gone; a successful swap is committed, and a setup failure after it is a config problem, not grounds to roll back a verified bundle

- Setup's provider screen now runs on charm.land/huh v2 (Bubble Tea v2) instead of hand-rolled raw-mode terminal code — first step of migrating the wizard's prompts to a maintained, cross-platform TUI stack. Same semantics: "Done" pre-highlighted, Esc skips/backs out (without testing a stored key), Ctrl+C exits with the "progress is saved" note; piped/non-tty runs use huh's accessible numbered fallback (API keys fall back to a visible line read, as before). Requires Go ≥ 1.25.8 to build
- Setup wizard streamlined for Enter-through: the provider menu pre-highlights "Done" (after a separator) so the offline default is one Enter, Esc cancels the API-key prompt without testing whatever key is stored (a mis-clicked provider no longer forces a key test), and the "Active provider" question is gone — local is the default engine and the tray switches providers any time
- Setup runs from a neutral working directory when installed as Zee.app, so launching it from `~/Desktop`/`~/Documents` no longer triggers a "Zee wants to access files in your Desktop folder" prompt
- Setup's paste test declares bracketed paste for the synthesized Cmd+V, so paste-protection terminals (Ghostty) no longer pop a confirmation dialog over the test token's newline; the received text is echoed by the wizard itself, identical in every terminal
- Ctrl+C anywhere in setup now prints "everything configured so far is saved — re-run `zee setup` to continue" before exiting (every step already persisted as it completed; now it says so)

- macOS colored tray icons (recording/warning/transcribing) now adapt to the menu bar theme: each ships light+dark glyph variants and the right one is picked when the icon changes — previously the white glyph was invisible on a light menu bar, leaving only the state dot

- `install.sh` now installs atomically: the new bundle is copied and un-quarantined in a hidden staging dir on the same volume, then swapped into place with two fast renames, keeping the old bundle as a backup until the swap succeeds (restored on failure). A Ctrl+C mid-install can no longer leave a half-copied app — the download is killed (no orphaned curl), a partial staging copy is discarded, and an interruption in the swap window restores the previous app rather than leaving nothing

- `install.sh` refuses upfront when a Zee instance is running ("quit it first") instead of trying to quit it via AppleScript — `tell application "Zee" to quit` would *launch* Zee if it wasn't running (to deliver the event) and trigger an Automation permission prompt, and a denied prompt left a running instance mid-`rm -rf`

- Reject unknown hotkey modifier names (e.g. `"alt"` for `"option"`) instead of silently dropping them — a dropped modifier left the bare key (e.g. Space) registered as a global hotkey. Validated on both registration and live rebind
- Hotkey validation hardened on all platforms: Linux startup registration now rejects a modifier-free combo (previously only rebind did — a hand-edited `"mods": []` bound bare Space globally), and out-of-range key codes are rejected instead of silently truncating to a different key
- Live `language` edits in `config.json` now reach the transcriber on Linux — the callback fired only from the macOS menu-render path, a no-op off-macOS
- Linux/Windows binaries always use the OS-standard per-user config dir — the dev "keep state next to the binary" split is now macOS-only, so a packaged `/usr/local/bin/zee` no longer tries to write `/usr/local/bin/.zee` (where saves silently failed)
- Live config edits naming an unknown provider now log a warning instead of being silently ignored
- Alert dialogs escape `\r` too (a raw CR broke the AppleScript literal the same way `\n` did)
- Diagnostics log rotation works repeatedly on Windows (rename can't replace an existing `.old` there)
- Setup's paste test restores the clipboard even when it was empty, instead of leaving the test token behind
- A saved hotkey that can't be registered no longer bricks every launch: a non-default combo falls back to the default (`⌃⇧Space`) with a tray warning to re-bind, instead of a fatal error loop; only a failing default is fatal
- Live config edits that set a provider without a model (what the setup wizard itself writes, and a natural hand-edit) now switch to the provider's default model instead of being silently ignored

- Replace the wizard's `open -W` relaunch with a TCC-disclaimed self-respawn (`responsibility_spawnattrs_setdisclaim`, the Chromium/LLDB approach): permission prompts still say "Zee" when `zee setup`/`zee doctor` run from a terminal as the installed bundle, but stdio and exit codes now inherit naturally — no LaunchServices, no tty wiring, no status-file side channel, and the "Zee.app was already running" self-deadlock class is gone entirely. The private SPI is resolved at runtime via `dlsym` (not direct-linked), so a future macOS that removes it degrades to running the wizard in place rather than failing to launch. Dev builds are unchanged (still terminal-attributed, so grants survive rebuilds)

- Fail fast when the network is unreachable: the HTTP client now bounds each phase (dial 10s, TLS 10s, response headers 60s) and the Deepgram websocket handshake gets a 15s deadline — an offline/dead-VPN dictation errors out in seconds instead of pinning the "transcribing" icon for the full 2-minute cap. Alert dialogs also render reliably now: messages containing quotes/newlines (every network error) broke the AppleScript literal and the dialog silently never appeared
- Deepgram no longer offers "Auto-detect" — its streaming API has no real language detection (omitting the language means English-only; `multi` covers just 10 languages), so an Auto intent now falls back to English while switching to Deepgram, and the saved Auto preference is restored on Whisper-class providers. A stale `language: ""` reaching the stream anyway (old config, headless flags) is sent as `language=multi` instead of silently going English-only, which returned empty finals ("no speech") for non-English dictation
- The setup wizard's provider screen no longer re-tests a key that was already verified this run when you select the provider and keep the existing key (Enter) — only a changed or not-yet-verified key triggers the live test
- Paste failures (revoked Accessibility, pbcopy errors) are now logged instead of silently swallowed
- The auto-save of a failed recording now completes before the transcription cycle ends (only the error dialog stays async), so quitting right after a failure can no longer lose the sample
- Streaming (Deepgram) sessions now retain the session audio and return it as WAV in the session result, so "Save Last Recording" and the auto-save-on-error flow work for streams too. Previously a failed stream (e.g. offline) lost the dictation entirely — no sample saved, no error dialog with the path — and after a successful stream dictation "Save Last Recording" silently saved a stale earlier clip
- Remove the `-debug` flag: diagnostic logging is always on (it had defaulted to on for a while, making the flag vestigial). Log files are now rotated at 10 MB (one `.old` generation kept) so always-on logging can't grow unbounded. `-debug-transcribe` stands alone for transcription text logging
- The saved microphone preference survives sessions started while the device is unplugged: plugging it in later now auto-switches to it (previously the preference was forgotten until restart)

- A failed transcription now auto-saves its recording to `samples/` (audio + `info.json`, which gains an `error` field), so a lost dictation — e.g. a long request Groq drops mid-flight — can be recovered or re-run through `-transcribe`. The failure shows an error alert saying what happened and where the audio went (previously the auto-save reused the manual save's "Saved to…" notice and the error hid in the tray tooltip). The "Save Last Recording" tray item is now always shown (no longer gated behind `ZEE_SAVE_LAST_AUDIO`), and both paths share the same save code; the local (Parakeet) path always retains a WAV copy of the last recording
- Add an interactive `zee -setup` wizard that configures a working install end to end and proves each piece as it goes: the microphone by recording a real utterance and transcribing it with the local model ("is that what you said?"), the push-to-talk hotkey by requiring one live fire (registration alone passes for system-owned combos like ⌘Space that never fire), Accessibility by polling until macOS reports the binary trusted, and each cloud API key by sending real audio through the provider (the mic-test recording, or silence). Any number of providers can be configured in one run; the active one is chosen at the end. Permissions are prompted from the app itself so macOS attributes them to Zee; the wizard is idempotent (re-run any time) and, on macOS, re-execs as the installed bundle so prompts attribute correctly. The push-to-talk hotkey is user-configurable and persisted in `config.json` (the default `⌃⇧Space` is seeded when the user keeps it)
- Fix the wizard's instance lifecycle: `zee setup`/`zee doctor` now refuse to start while a tray instance is running ("quit it first", exit 1) instead of silently killing it — checked before the `open` re-exec, which would otherwise activate the running tray, ignore `--args`, and block forever; the post-setup launch uses `open -n` so it starts a fresh tray instead of activating the exiting wizard
- Auto-paste in the wizard is now verified end to end: after the Accessibility grant, a real synthesized paste is sent at the wizard's own terminal and must arrive on its stdin; failing that loops (retry) or disables auto-paste. `zee doctor` runs the same paste check when auto-paste is enabled. The Accessibility step also deep-links the System Settings pane (the one-shot AX prompt is easy to lose), via new shared `permissions.OpenAccessibilitySettings`/`OpenMicrophoneSettings` (main.go's startup warning now uses the same helper)
- The provider screen verifies every already-stored key with real audio as it opens, so the ✓ marks mean "authenticated now", not "a key exists"
- Remove the `-doctor` flag; its checks are now the setup wizard's verification pass
- Add bare subcommands `zee setup` and `zee doctor` (the `-setup` flag stays as an alias). `zee doctor` is a zero-question health check: it registers the saved hotkey, has you hold it and speak — one real push-to-talk cycle — transcribes with the configured provider, and reports hotkey/mic/accessibility/provider with an exit code; fixes belong to `zee setup`
- `install.sh` now hands off to `zee -setup` after installing (interactive terminals only), replacing the `launchctl setenv` API-key instructions; a running instance must be quit first (see the refusal entry above)
- `install.sh` downloads the offline models *first* and aborts the install if they can't be fetched (previously a silent best-effort after the app copy) — the offline promise is part of the product, and a failure now leaves the system untouched instead of half-installed
- Add a tray "Settings → Edit Settings…" item that opens `config.json`, and a disabled "Hotkey: <combo>" line showing the current push-to-talk binding
- "Edit Settings…" now materializes an unset hotkey as the active default (`⌃⇧Space`) in `config.json`, so the opened file shows a readable combo instead of `"key": 0` / empty label
- `config.json` edits now apply live: a watcher polls the file (1s) and applies every field through the same guarded paths the tray uses — hotkey (validated `Rebind`, kept on failure), provider/model (ready models only, deferred to cycle end mid-recording), device, language, auto-paste, and start-on-login — with tray checkmarks, the hotkey label, and the Start/Stop Recording hint refreshed to match. The app's own saves are suppressed by content comparison
- Cloud provider API keys now live in a `credentials.json` (0600) beside `config.json`, read by the app at startup regardless of how it's launched — the `*_API_KEY` environment variables are no longer read (breaking: existing users must re-add their key). The login-item plist no longer bakes keys into its environment. Dev builds keep all state self-contained in `<binary dir>/.zee`, isolated from the installed app; `ZEE_CONFIG_DIR` overrides the config location
- `install.sh` reads the model list (filenames, hashes, prefetch flags) from a generated `localmodel/manifest.txt` instead of a hand-copied list, making `localmodel.go` the single source of truth; a test fails the build if the manifest drifts. Renamed `cmd/modeldl` → `cmd/localmodels` with `download` and `manifest` subcommands
- Fix a data race on the config directory: the config watcher goroutine read the package-global `dir` while `SetDir`/`Load` wrote it (caught by CI's race detector); `dir` is now lock-guarded and `Watch` returns a stop func so leaked pollers can't cross test boundaries (a leaked watcher could steal another test's reload and starve its callback)
- Fix data races on tray state: a single `trayMu` now guards the recording flag, model list, device list, and language fields (previously the language/recording fields had no lock while the model-switch goroutine and the systray click thread both touched them)
- Fix a data race on the selected/preferred microphone: the device-monitor goroutine read them unlocked while the tray callback wrote them; both now go through `captureMu`
- Fix dictation lost when a model switch (tray menu or a finished background download) lands mid-inference: the switch is now deferred to the end of the record/transcribe cycle instead of freeing the model an in-flight session is using
- Free the outgoing local (Parakeet) model when switching provider, instead of leaking its 255 MB–1.4 GB of C/ggml memory for the life of the app
- Tray "Start Recording" during inference is now denied (like the hotkey) instead of queuing an unattended recording that fired the moment inference ended; both paths share one guarded entry point
- Fix a race in the recording stop machinery (`requestStop` touched the stop channel/Once without the lock held)
- `install.sh` downloads draw a compact fixed-width `[····      ] 53% 4/7 MB` progress line that redraws in place (curl's own bar stacked lines — one bar per redirect hop, plus wrapping in narrow panes), and interrupted downloads now resume from the leftover `.part` instead of restarting (safe: release assets are immutable and the checksum still gates the final file); a checksum mismatch (stale/corrupt partial) retries once from scratch instead of aborting the install
- `install.sh` accepts `DMG_PATH=<file>` to install a locally built DMG (`make app`), running the identical flow minus download/checksum — for testing the install UX end to end before a release
- `install.sh` refuses to install on Intel Macs (releases are Apple-Silicon-only) instead of "succeeding" with a binary that can't launch
- Local models now resolve to an absolute directory instead of a cwd-relative path: dev builds use the versioned folder next to the binary (found from any working directory), installed apps use `<config>/models`, so a binary launched from elsewhere no longer reports downloaded models as missing (dev override via `ZEE_MODELS_DIR` unchanged)
- Model downloads abort after 60s with no data (stalled connection) instead of wedging the tray menu at "downloading N%" until restart
- `install.sh` verifies pre-fetched model downloads against the release's `checksums.txt` instead of hashes hardcoded in the script; add `make model-release MODELS_DIR=… MODELS_TAG=models-vN` to publish the GGUF models (generates `checksums.txt`, uploads with `--latest=false` so the app-release "latest" pointer can't be hijacked)

- Fix the "no engine available" startup error listing only 3 cloud keys: it now surfaces all five providers plus the offline-on-Apple-Silicon hint (the message is no longer hardcoded and can't drift as providers change)

- Fix language preference being clobbered by English-only models: switching to a model that can't offer the selected language (e.g. Parakeet English-only while on Auto-detect or Turkish) no longer overwrites the saved choice. The fallback is applied transiently; switching back to a capable model restores the original language

- Fix Auto-detect language not persisting: selecting Auto-detect (empty language code) was coerced back to English on the next launch. Settings now load over defaults so an explicit empty language is kept while an unset one still defaults to English

- Add offline, on-device transcription via Parakeet (parakeet.cpp, CPU) on Apple Silicon
- Works out of the box with no API key on Apple Silicon (falls back to the local 110M model)
- Add local model picker in the tray: 110M English (default), 0.6B v3 multilingual, 0.6B v2 English (opt-in download)
- Download missing local models on demand from the tray with progress
- `-transcribe` supports local WAV (16 kHz mono) transcription without a network call, and accepts multiple files in one invocation (model loaded once; one transcript printed per line)
- Block starting a new recording while the previous transcription is still in progress (the recording guard now spans inference, not just capture); show a blue status-dot tray icon during transcription and play a short "denied" beep if the hotkey is pressed then
- `-doctor` reports local model status (present, path, size, decoder)
- `-doctor` transcription test uses the app's default engine (local Parakeet, else first cloud key) instead of prompting for a provider + API key
- Idle tray icon adapts to the menubar appearance (template tinting) — renders white on dark/transparent menubars instead of black
- Diagnostics log per-transcription process RSS (`rss_mb`, from gopsutil — includes cgo/mmap model memory) for both batch and stream sessions
- "Save Last Recording" now works for the local (Parakeet) model — captured PCM is saved as WAV (was cloud-only before)
- Add `-provider` and `-model` flags to override the saved provider/model from the CLI (an unavailable explicit `-provider` is now a hard error)
- Fix recording immediately aborting ("Start" auto-releases) after the selected microphone was unplugged: the record loop kept using a stale reference to the removed device instead of the system-default it was switched to. It now reads the current capture device for each recording (device swaps guarded by a mutex)
- Fix a crash (SIGSEGV/SIGABRT double-free in `ma_device_uninit`) when recording after a sleep/wake or audio-device change: the per-call device reinit left the device pointer dangling when reinit failed, so the next call uninited the already-freed device. Null the device after uninit in both capture and beep playback. Also serialize all miniaudio device lifecycle calls behind a process-wide lock as defense against concurrent capture/playback init/uninit
- Merge the `beep` package into `audio` so one package owns both capture and playback feedback tones: the miniaudio lifecycle lock is now a private `audio` detail shared by both directions (was the exported `internal/malgolock`), and capture/playback share one malgo context instead of two. Removes `beep/` and `internal/malgolock/`
- Fix the tray language menu to always reflect the active model's languages — both at startup and on model switch. English-only Parakeet models no longer offer Auto-detect, and switching providers (e.g. to Groq) now updates the list

## v0.3.8

- Update dialog points to install instructions


## v0.3.7

- Replace Homebrew cask distribution with DMG installer script
- Add release workflow support for DMG checksum verification
- Add vocabulary hints support for transcription providers
- Add Deepgram keyterm support for hints
- Add optional last-recording sample export for debugging
- Remove dead streaming and dev-only CLI flags

## v0.3.6

- Add persistent settings for language, device, provider/model, auto-paste, and auto-start
- Add ElevenLabs Scribe transcription provider
- Replace self-update patching with Homebrew/release-page update guidance
- CLI flags override persisted settings when explicitly passed
- Fix hotkey unable to stop tray-initiated recordings (global stop channel replaces per-session channels)

## v0.3.5

- Add Mistral Voxtral batch transcription provider
- Add per-model language filtering in the tray menu
- Add startup Accessibility warning for missing/stale auto-paste permission
- Alert dialogs for all user-visible errors/warnings (no more invisible stdout messages in .app mode)
- Use `alert.Error()` for fatal errors and `alert.Warn()` for non-fatal warnings
- GitHub Actions updated to checkout@v5 and setup-go@v6

## v0.3.1

- Code-sign app bundle with stable identifier (`com.zee.app`) to prevent repeated permission prompts

## v0.3.0

- Add macOS DMG packaging via `make app`
- Add OpenAI Whisper provider
- Add tray language menu
- Add separate transcription logging with `-debug-transcribe`
- Add visible alert dialogs for init errors
- Add tray auto-paste toggle
- Add login item support
- Make system tray mode the only UI mode
- Enable hybrid tap/hold hotkey mode by default
- Show checkmark for active microphone in tray menu
- Use consistent tray/app icon
- Tune VAD threshold for better silence detection
- Fix stale stopCh after tray cancel
- Fix integration tests
- Harden login item security and device selection
- Move Stream flag to transcriber ModelInfo for cleaner provider abstraction

## v0.2.0

- VAD-based silence detection with warnings and auto-close
- System tray mode with dynamic icons
- Bluetooth headset warning
- Auto-close on prolonged silence

## v0.1.5

Initial tagged release with core push-to-talk transcription.
