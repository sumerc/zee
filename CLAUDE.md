# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

**Note:** zee - push-to-talk transcription app. Runs as a system tray icon on macOS.

## Build & Run

```bash
make build                            # build
GROQ_API_KEY=xxx ./zee            # run (hold Ctrl+Shift+Space to record)
```

## Testing

```bash
make test                             # unit tests
make integration-test WAV=test/data/short.wav  # requires GROQ_API_KEY
make benchmark WAV=file.wav RUNS=5
```

## Flags

- `-stream` - enable streaming transcription (Deepgram only)
- `-debug` - enable diagnostic and transcription logging (default: false)
- `-hybrid` - tap-to-toggle + hold-to-talk on the same hotkey
- `-format <mp3@16|mp3@64|flac>` - audio format (default: mp3@16)
- `-lang <code>` - language code for transcription (default: en, also settable from tray menu)
- `-device <name>` - use named microphone device (also switchable from tray menu)
- `-setup` - select microphone device interactively
- `-doctor` - run system diagnostics and exit
- `-benchmark <wav>` - run benchmark instead of live recording
- `-runs N` - benchmark iterations (default: 3)
- `-logpath <path>` - log directory (default: `$ZEE_LOG_PATH` or OS-specific, use `./` for current directory)

## Architecture

Push-to-talk transcription using Groq Whisper API:

```
Ctrl+Shift+Space keydown → record audio → encode (mode-based) → API call → clipboard
```

**Files:**
- `main.go` - hotkey handling, audio capture, recording logic, panic recovery
- `tray/` - system tray icon, menus (devices, providers, languages, auto-paste), dynamic icons
- `encoder/` - AudioEncoder interface, FLAC, MP3, and Adaptive implementations
- `transcriber/` - Groq and DeepGram API clients with shared TracedClient for HTTP timing metrics
- `hotkey/` - global hotkey registration (Ctrl+Shift+Space) with platform-specific backends
- `clipboard/` - platform-specific clipboard and paste operations (Cmd+V / Ctrl+V)
- `audio/` - platform-specific audio capture (malgo on macOS, PulseAudio on Linux)
- `beep/` - platform-specific audio playback for feedback sounds
- `doctor/` - system diagnostics (`-doctor` flag)
- `internal/mp3/` - vendored shine-mp3 encoder (with mono fix)
- `device.go` - microphone picker with arrow-key navigation
- `vad.go` - voice activity detection using WebRTC VAD with debounced speech confirmation
- `silence.go` - silence monitoring with warnings, repeat beeps, and auto-close (toggle mode)
- `log.go` - diagnostic logging and panic capture to `diagnostics_log.txt`

## Design Philosophy

- **Unix philosophy packages** - Each subfolder is a self-contained utility that does one thing: `beep/` plays sounds, `clipboard/` copies and pastes, `audio/` captures mic input, `transcriber/` talks to STT APIs, `hotkey/` registers global keys. They expose a minimal interface and hide all platform/provider details behind build tags.
- **Root files are pure business logic** - `main.go` and other root files orchestrate the workflow but never import OS-specific APIs or know implementation details of subpackages. When `main.go` calls `clipboard.Paste()`, it doesn't know whether that's pbcopy, xclip, or Win32 — and it shouldn't. Same for `beep.PlayEnd()`, `audio.Start()`, `transcriber.Transcribe()`, etc.
- **No leaky abstractions** - Never add provider-specific, OS-specific, or library-specific logic to root files. If a new STT provider needs special handling, that belongs in `transcriber/`. If a new platform needs a different paste mechanism, that belongs in `clipboard/`.
- **Shared constants in one place** - No duplicate magic numbers; extract to package-level constants.

**Key design:**
- Streaming encoder runs concurrently during recording (not after)
- HTTP keep-alive reuses TLS connections across requests
- Connection pre-warming on startup reduces first-request latency
- Output shows detailed timing breakdown (dns/tls/network/inference)
- Panics are captured to `diagnostics_log.txt` with full stack trace

**Log files:**
- Default location: OS-specific (macOS: `~/Library/Logs/zee/`, Linux: `~/.config/zee/logs/`, Windows: `%LOCALAPPDATA%\zee\logs\`)
- Override with `ZEE_LOG_PATH` env var or `-logpath <path>` flag (supports relative paths, use `./` for current directory)
- `crash_log.txt` - panic recovery (always enabled)
- `diagnostics_log.txt` - timing metrics, errors, warnings (requires `-debug`)
- `transcribe_log.txt` - transcription text history (requires `-debug`)
