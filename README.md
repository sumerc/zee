<p align="center">
  <img src="eye.gif" alt="zee" width="264"><br>
  <strong>zee</strong><br><br>
  Voice transcription that stays out of your way.<br>
  Push-to-talk, tap-to-toggle, or real-time streaming. Pure Go. Sub-second fast.<br><br>
  <img src="https://img.shields.io/badge/go-1.24-00ADD8?logo=go&logoColor=white" alt="Go 1.24">
  <img src="https://img.shields.io/badge/platform-macOS-lightgrey?logo=apple" alt="macOS">
  <a href="https://freepalestine.dev"><img src="https://freepalestine.dev/badge?t=d&u=0&r=1" alt="From the river to the sea, Palestine will be free"></a>
</p>

## Highlights

- **Three recording modes** — push-to-talk (hold hotkey), tap-to-toggle (tap to start/stop), or hybrid (both on the same key via `-hybrid`).
- **Real-time streaming** — with `-stream`, words appear as you speak and auto-paste into the focused window incrementally. Powered by Deepgram's WebSocket API.
- **Fast batch mode** — HTTP keep-alive, TLS connection reuse, pre-warmed connections, streaming encoder runs during recording (not after). Typical key-release to clipboard: under 500ms.
- **Auto-paste** — transcribed text goes straight to clipboard and pastes into the active window. In streaming mode, each new phrase pastes as it arrives.
- **Pure Go encoding** — MP3 and FLAC encoders, no CGO. Three formats: `mp3@16` (smallest), `mp3@64` (balanced), `flac` (lossless).
- **Multiple providers** — Groq Whisper and Deepgram, switchable at runtime.
- **Cross-platform** — minimal dependencies, pure Go where possible.
  - [x] macOS
  - [ ] Linux
  - [ ] Windows
- **[HAL 9000](https://en.wikipedia.org/wiki/HAL_9000) TUI** — voice-reactive animated eye with live transcription and timing metrics (`-expert`).

## Screenshots

<p align="center">
  <img src="screenshot.png" alt="zee TUI" width="680">
</p>

## Install

### macOS

```bash
curl -L -o zee "https://github.com/sumerc/zee/releases/latest/download/zee_darwin_$(uname -m)"
chmod +x zee
sudo mv zee /usr/local/bin/
```

## Usage

```bash
export GROQ_API_KEY=your_key    # batch mode (Groq Whisper)
zee                              # hold Ctrl+Shift+Space to record
```

```bash
export DEEPGRAM_API_KEY=your_key # streaming mode (Deepgram)
zee -stream                      # words appear as you speak
```

Hold `Ctrl+Shift+Space` to record, release to transcribe. Result auto-pastes into the focused window.

With `-hybrid`, a short tap toggles recording on/off (hands-free) while a long press works as push-to-talk.

Use `-setup` to pick a microphone, otherwise uses system default.

### macOS Permissions

On first run, macOS will prompt you to grant permissions to your terminal app (Ghostty, iTerm2, Terminal.app, etc.):

1. **Microphone** — Required for audio recording. Go to System Settings → Privacy & Security → Microphone and enable your terminal.

2. **Accessibility** — Required for global hotkey and auto-paste. Go to System Settings → Privacy & Security → Accessibility and enable your terminal.

If permissions aren't granted, zee will fail silently or the hotkey won't register. Run with `-doctor` to diagnose permission issues.

## Testing

```bash
make test                                      # unit tests
make integration-test WAV=test/data/short.wav  # requires GROQ_API_KEY
make benchmark WAV=file.wav RUNS=5             # multiple runs for timing
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-stream` | false | Real-time streaming transcription (Deepgram) |
| `-format` | mp3@16 | Audio format: `mp3@16`, `mp3@64`, or `flac` |
| `-hybrid` | false | Tap-to-toggle + hold-to-talk on the same hotkey |
| `-longpress` | 350ms | Threshold distinguishing tap vs hold |
| `-autopaste` | true | Auto-paste into focused window |
| `-setup` | false | Select microphone device |
| `-lang` | (auto) | Language code (e.g., `en`, `es`, `fr`) |
| `-expert` | false | Full TUI with HAL eye and timing metrics |
| `-doctor` | false | Run system diagnostics and exit |
| `-logpath` | OS-specific | Log directory (use `./` for current dir) |
| `-profile` | - | pprof server address (e.g., `:6060`) |
| `-benchmark` | - | WAV file for benchmarking |
| `-runs` | 3 | Benchmark iterations |
| `-version` | false | Print version and exit |

## About

Vibe-coded in ~30 hours with AI, but built with love and care. The kind of polish you get when you actually use the thing you're building.
