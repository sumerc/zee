<p align="center">
  <img src="zee-logo-stack.svg" alt="zee" width="180"><br>
  <strong>zee</strong><br><br>
  Voice transcription that stays out of your way.<br>
  Supports Groq, OpenAI, Mistral, ElevenLabs and Deepgram models.<br>
  Push-to-talk, tap-to-toggle, or real-time streaming. Pure Go. Sub-second fast.<br><br>
  <img src="https://img.shields.io/badge/go-1.24-00ADD8?logo=go&logoColor=white" alt="Go 1.24">
  <img src="https://img.shields.io/badge/platform-macOS-lightgrey?logo=apple" alt="macOS">
  <a href="https://freepalestine.dev"><img src="https://freepalestine.dev/badge?t=d&u=0&r=1" alt="From the river to the sea, Palestine will be free"></a>
</p>

<p align="center">
  <img src="zee-on-action.gif" alt="zee in action" width="600">
</p>

## Highlights

- **Offline, on-device** — on Apple Silicon, transcribes fully locally via Parakeet (parakeet.cpp, CPU) with **no API key and no network**. Cloud providers are optional and switchable from the tray.
- **System tray app** — lives in the menu bar. Switch microphones, transcription providers, and languages from the tray menu. Dynamic icons show recording and warning states.
- **Two recording modes** — push-to-talk (hold hotkey) or tap-to-toggle (tap to start/stop).
- **Real-time streaming** — when a streaming-capable model is selected (e.g. Deepgram Nova-3), words appear as you speak and auto-paste into the focused window incrementally.
- **Fast batch mode** — HTTP keep-alive, TLS connection reuse, pre-warmed connections, streaming encoder runs during recording (not after). Typical key-release to clipboard: under 500ms.
- **Auto-paste** — transcribed text goes straight to clipboard and pastes into the active window. In streaming mode, each new phrase pastes as it arrives.
- **Silence detection** — VAD-based voice activity detection warns when no speech is heard. In streaming mode, auto-closes recording after 30 seconds of silence.
- **Pure Go encoding** — MP3 and FLAC encoders, no CGO. Three formats: `mp3@16` (smallest), `mp3@64` (balanced), `flac` (lossless).
- **Multiple providers** — Groq, OpenAI, Mistral, ElevenLabs, and Deepgram, switchable from the tray menu at runtime.
- **36 languages** — select transcription language from the tray menu or via `-lang` flag.
- **Cross-platform** — minimal dependencies, pure Go where possible.
  - [x] macOS (Apple Silicon)
  - [ ] Linux
  - [ ] Windows

## Install

### One-liner (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/sumerc/zee/main/install.sh | bash
```

Downloads the latest DMG, verifies its SHA256 against `checksums.txt`, copies `Zee.app` to `/Applications`, and clears the quarantine attribute. Pin a version with `VERSION=vX.Y.Z bash`.

### Manual DMG

1. Download `Zee-<version>.dmg` from the [latest release](https://github.com/sumerc/zee/releases/latest)
2. Open the DMG and drag **Zee.app** to **Applications**
3. Clear quarantine: `xattr -cr /Applications/Zee.app`

### CLI binary

For terminal usage:

```bash
# Apple Silicon (the only supported target)
curl -L https://github.com/sumerc/zee/releases/latest/download/zee_darwin_arm64.tar.gz | tar xz
```

```bash
./zee                               # offline, on-device (no key needed)
./zee setup                         # add cloud providers (Groq, OpenAI, Deepgram, …), pick mic + hotkey
./zee -debug-transcribe             # include transcription text logs
```

> **Note:** When running from a terminal, macOS permissions (Microphone, Accessibility) are granted to the **terminal app** (e.g. Ghostty, iTerm2, Terminal), not to zee itself.

### Update

Use **Check for Updates** from the tray, or run:

```bash
/Applications/Zee.app/Contents/MacOS/zee update
```

Zee verifies the release archive, replaces `Zee.app`, and restarts. Models and settings are unchanged.

### Build from source

Requires **Apple Silicon**, plus `cmake` and the Xcode Command Line Tools (for the one-time on-device STT engine build).

```bash
git clone https://github.com/sumerc/zee && cd zee
make build        # builds the local STT engine (cmake) + CLI binary;
                  # first run also fetches the default models (~900 MB) into models/parakeet/v1/
make app          # macOS DMG
```

The submodule, static libraries, and models are all set up automatically by `make build` — no manual steps.

## Usage

On Apple Silicon, zee works offline out of the box — no key required. To use a cloud provider (Groq Whisper, OpenAI, Deepgram streaming, Mistral Voxtral, ElevenLabs Scribe), run the setup wizard and paste the provider's API key when prompted:

```bash
zee setup
```

Keys are stored per-provider in `credentials.json` (mode 0600) in zee's config directory — environment variables are not read. Each key is live-tested as you enter it, and you can switch providers any time from the tray.

zee runs as a system tray app in the menu bar. Hold `Ctrl+Shift+Space` (the default — rebindable in `zee setup`) to record, release to transcribe. Result auto-pastes into the focused window.

Use the tray menu to switch microphones, providers, and languages.

### macOS Permissions

On first run, macOS will prompt for permissions:

1. **Microphone** — Required for audio recording. System Settings → Privacy & Security → Microphone.

2. **Accessibility** — Required for global hotkey and auto-paste. System Settings → Privacy & Security → Accessibility.

If permissions aren't granted, zee will fail silently or the hotkey won't register. Run `zee -setup` to (re-)grant permissions and verify everything works.

## Testing

```bash
make test                                      # unit tests
make test-integration                          # integration tests (builds binary, requires GROQ_API_KEY)
make integration-test WAV=test/data/short.wav  # single-file integration test (requires GROQ_API_KEY)
make benchmark WAV=file.wav RUNS=5             # multiple runs for timing
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-format` | mp3@16 | Audio format: `mp3@16`, `mp3@64`, or `flac` |
| `-autopaste` | true | Auto-paste into focused window |
| `-setup` | false | Run the interactive setup wizard (provider, key, device, permissions, hotkey) and exit |
| `-device` | (default) | Use named microphone device |
| `-lang` | en | Language code (e.g., `en`, `es`, `fr`) |
| `-debug-transcribe` | false | Enable transcription text logging |
| `-logpath` | OS-specific | Log directory (use `./` for current dir) |
| `-hints` | - | Vocabulary hints for transcription (comma-separated) |
| `-transcribe` | - | Audio file to transcribe and exit |
| `-benchmark` | - | WAV file for benchmarking |
| `-runs` | 3 | Benchmark iterations |
| `-version` | false | Print version and exit |

## Environment

| Variable | Description |
|----------|-------------|
| `ZEE_LOG_PATH` | Log directory override |
| `ZEE_PPROF` | pprof server address (e.g., `:6060`) |
| `ZEE_CRASH=1` | Trigger synthetic crash for crash-log testing |
| `ZEE_LONGPRESS_DURATION` | Hybrid hotkey long-press threshold (e.g., `350ms`) |

## About

Started as a vibe-coding project but turned into a standalone app I use daily for all my speech-to-text. Built with AI, love, and care — the kind of polish you get when you actually use the thing you're building.
