<p align="center">
  <img src="zee-logo-stack.svg" alt="zee" width="180"><br>
  <strong>zee</strong><br><br>
  Voice transcription that stays out of your way.<br>
  <strong>Runs fully on-device — no account, no API key, no network.</strong><br>
  Local Parakeet and Whisper on Metal, or Groq, OpenAI, Mistral, ElevenLabs and Deepgram.<br>
  Push-to-talk, tap-to-toggle, or real-time streaming. Pure Go. Sub-second fast.<br><br>
  <img src="https://img.shields.io/badge/go-1.24-00ADD8?logo=go&logoColor=white" alt="Go 1.24">
  <img src="https://img.shields.io/badge/platform-macOS-lightgrey?logo=apple" alt="macOS">
  <a href="https://freepalestine.dev"><img src="https://freepalestine.dev/badge?t=d&u=0&r=1" alt="From the river to the sea, Palestine will be free"></a>
</p>

<p align="center">
  <img src="zee-on-action.gif" alt="zee in action" width="600">
</p>

## Highlights

- **Offline, on-device** — on Apple Silicon, transcribes fully locally with **no API key and no network**, working from the first launch. Two engines, both GPU-accelerated (Metal): **Parakeet** for fast English, **Whisper** (large-v3 turbo) for multilingual with auto-detect. Cloud providers are optional and switchable from the tray.
- **System tray app** — lives in the menu bar. Switch microphones, transcription providers, and languages from the tray menu. Dynamic icons show recording and warning states.
- **Two recording modes** — push-to-talk (hold hotkey) or tap-to-toggle (tap to start/stop).
- **Real-time streaming** — when a streaming-capable model is selected (e.g. Deepgram Nova-3), words appear as you speak and auto-paste into the focused window incrementally.
- **Fast batch mode** — HTTP keep-alive, TLS connection reuse, pre-warmed connections, streaming encoder runs during recording (not after). Typical key-release to clipboard: under 500ms.
- **Auto-paste** — transcribed text goes straight to clipboard and pastes into the active window. In streaming mode, each new phrase pastes as it arrives.
- **Silence detection** — VAD-based voice activity detection warns when no speech is heard. In streaming mode, auto-closes recording after 30 seconds of silence.
- **Pure Go encoding** — MP3 and FLAC encoders, no CGO. Three formats: `mp3@16` (smallest), `mp3@64` (balanced), `flac` (lossless).
- **Multiple providers** — local Parakeet and Whisper, plus Groq, OpenAI, Mistral, ElevenLabs, and Deepgram, switchable from the tray menu at runtime.
- **36 languages** — select transcription language from the tray menu or via `-lang` flag.
- **Cross-platform** — minimal dependencies, pure Go where possible.
  - [x] macOS (Apple Silicon)
  - [ ] Linux
  - [ ] Windows

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/sumerc/zee/main/install.sh | bash
```

That's everything — models included, nothing to configure. macOS asks for
Microphone and Accessibility the first time you record; grant both and you're
done.

## Use

Hold **⌥Space** to record, release to transcribe. The text lands in your
clipboard and pastes into whatever window you're in.

Microphone, provider, language, and hotkey all live in the tray menu. To add a
cloud provider (Groq, OpenAI, Deepgram, Mistral, ElevenLabs), run `zee setup` —
it live-tests the key as you paste it.

## Update

Quit zee from the menu bar, then:

```bash
/Applications/Zee.app/Contents/MacOS/zee update
```

The tray's **Check for Updates** tells you when a release is out; this command is
what installs it.

## Docs

- [**Reference**](docs/reference.md) — flags, environment variables, config and log
  file layout, CLI install, building from source, testing and benchmarks.
- [**Design notes**](docs/design-notes.md) — *why* the non-obvious choices were
  made: which engine and model, which backend, what got measured, and which
  alternatives were tried and rejected. Worth reading before changing anything in
  `audio/`, `transcriber/`, or the local model registry.

## About

Started as a vibe-coding project but turned into a standalone app I use daily for all my speech-to-text. Built with AI, love, and care — the kind of polish you get when you actually use the thing you're building.
