# Changelog

## Unreleased

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
