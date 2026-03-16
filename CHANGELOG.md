# Changelog

## v0.3.5

### Added
- **Mistral Voxtral provider** — batch transcription via Voxtral Mini
- **Per-model language filtering** — tray language menu shows only languages supported by the active model
- **Accessibility check at startup** — warns if auto-paste permission is missing or stale

### Changed
- Alert dialogs for all user-visible errors/warnings (no more invisible stdout messages in .app mode)
- `alert.Error()` for fatal errors, `alert.Warn()` for non-fatal warnings with caution icon
- GitHub Actions updated to checkout@v5 and setup-go@v6

## v0.3.1

### Fixed
- Code-sign app bundle with stable identifier (`com.zee.app`) to prevent repeated permission prompts

## v0.3.0

### Added
- **macOS DMG packaging** — `make app` produces a drag-and-drop installable DMG with Zee.app bundle
- **OpenAI Whisper provider** — switchable from tray menu alongside Groq and Deepgram
- **Language menu** — 36 languages selectable from the tray menu
- **Separate transcription logging** — new `-debug-transcribe` flag for transcription text history, independent from diagnostic logging
- **Alert/fatal on init errors** — clear error dialogs instead of silent failures
- **Auto-paste toggle** — enable/disable from tray menu
- **Login item support** — auto-start zee on macOS login

### Changed
- **System tray only** — removed terminal UI mode, zee now runs exclusively as a menu bar app
- **Hybrid mode default** — `-hybrid` is now enabled by default (tap-to-toggle + hold-to-talk)
- **Default device checked** — tray menu shows checkmark on the active microphone
- **Consistent app icon** — tray icon and app icon now use the same black circle design
- **VAD threshold tuning** — decreased VAD threshold for better silence detection

### Fixed
- Fix stale stopCh after tray cancel
- Fix integration tests
- Harden login item security and device selection
- Move Stream flag to transcriber ModelInfo for cleaner provider abstraction

## v0.2.0

### Added
- VAD-based silence detection with warnings and auto-close
- System tray mode with dynamic icons
- Bluetooth headset warning
- Auto-close on prolonged silence

## v0.1.5

Initial tagged release with core push-to-talk transcription.
