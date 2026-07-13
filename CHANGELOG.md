# Changelog

## Unreleased

- A failed transcription now auto-saves its recording to `samples/` (audio + `info.json`, which gains an `error` field), so a lost dictation — e.g. a long request Groq drops mid-flight — can be recovered or re-run through `-transcribe`. The "Save Last Recording" tray item is now always shown (no longer gated behind `ZEE_SAVE_LAST_AUDIO`), and both paths share the same save code; the local (Parakeet) path always retains a WAV copy of the last recording
- Add an interactive `zee -setup` wizard that configures a working install end to end: transcription provider + API key, input device, auto-paste, Microphone and Accessibility permissions (prompted from the app itself so macOS attributes them to Zee), and a custom push-to-talk hotkey captured live and validated by real registration — then verifies the result. It is idempotent (re-run any time to change settings or re-grant permissions) and, on macOS, re-execs as the installed bundle so permission prompts attribute correctly. The push-to-talk hotkey is now user-configurable and persisted in `config.json`
- Remove the `-doctor` flag; its checks are now the setup wizard's verification pass
- `install.sh` now hands off to `zee -setup` after installing (interactive terminals only), replacing the `launchctl setenv` API-key instructions; it also quits a running instance before replacing the bundle
- Add a tray "Settings → Edit Settings…" item that opens `config.json`, and a disabled "Hotkey: <combo>" line showing the current push-to-talk binding
- "Edit Settings…" now materializes an unset hotkey as the active default (`⌃⇧Space`) in `config.json`, so the opened file shows a readable combo instead of `"key": 0` / empty label
- `config.json` edits now apply live: a watcher polls the file (1s) and applies every field through the same guarded paths the tray uses — hotkey (validated `Rebind`, kept on failure), provider/model (ready models only, deferred to cycle end mid-recording), device, language, auto-paste, and start-on-login — with tray checkmarks, the hotkey label, and the Start/Stop Recording hint refreshed to match. The app's own saves are suppressed by content comparison
- Cloud provider API keys now live in a `credentials.json` (0600) beside `config.json`, read by the app at startup regardless of how it's launched — the `*_API_KEY` environment variables are no longer read (breaking: existing users must re-add their key). The login-item plist no longer bakes keys into its environment. Dev builds keep all state self-contained in `<binary dir>/.zee`, isolated from the installed app; `ZEE_CONFIG_DIR` overrides the config location
- `install.sh` reads the model list (filenames, hashes, prefetch flags) from a generated `localmodel/manifest.txt` instead of a hand-copied list, making `localmodel.go` the single source of truth; a test fails the build if the manifest drifts. Renamed `cmd/modeldl` → `cmd/localmodels` with `download` and `manifest` subcommands
- Fix data races on tray state: a single `trayMu` now guards the recording flag, model list, device list, and language fields (previously the language/recording fields had no lock while the model-switch goroutine and the systray click thread both touched them)
- Fix a data race on the selected/preferred microphone: the device-monitor goroutine read them unlocked while the tray callback wrote them; both now go through `captureMu`
- Fix dictation lost when a model switch (tray menu or a finished background download) lands mid-inference: the switch is now deferred to the end of the record/transcribe cycle instead of freeing the model an in-flight session is using
- Free the outgoing local (Parakeet) model when switching provider, instead of leaking its 255 MB–1.4 GB of C/ggml memory for the life of the app
- Tray "Start Recording" during inference is now denied (like the hotkey) instead of queuing an unattended recording that fired the moment inference ended; both paths share one guarded entry point
- Fix a race in the recording stop machinery (`requestStop` touched the stop channel/Once without the lock held)
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
