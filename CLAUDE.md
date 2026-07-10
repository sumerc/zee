# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

**Note:** zee - push-to-talk transcription app. Runs as a system tray icon on macOS.

## Rules
- **CHANGELOG.md** — only log code/behavior changes. No docs, README, or comment-only updates. Be concise.
- **Clean package interface** — every package must expose a single, platform-neutral interface describing *what it provides*, defined once (typically in `<pkg>.go`). Public API, shared types, and guard logic live there; platform/provider files (build-tag variants) only implement the backend hooks. Never duplicate the public API across build-tag files (see `audio/`: `audio.go` owns the capture interface plus `PlayStart/PlayEnd/...`, platform files provide only the backends — `initSound`/`playOne` for playback, the malgo/pulse capture impls).

## Build & Run

```bash
make build                            # build binary
make app                              # build macOS DMG (binary + icns + .app bundle)
GROQ_API_KEY=xxx ./zee                # run (hold Ctrl+Shift+Space to record)
```

## Install

End users install via:

```bash
curl -fsSL https://raw.githubusercontent.com/sumerc/zee/main/install.sh | bash
```

`install.sh` downloads the latest DMG from GitHub Releases, verifies its SHA256 against `checksums.txt`, copies `Zee.app` to `/Applications`, and runs `xattr -cr` to clear quarantine. Permissions (Microphone, Accessibility) are still granted lazily by macOS TCC on first use — installers cannot pre-grant them.

Local dev DMG: `make app` produces `Zee-<version>.dmg`; drag to `/Applications`.

## Testing

```bash
make test                             # unit tests
make integration-test WAV=test/data/short.wav  # requires GROQ_API_KEY
make benchmark WAV=file.wav RUNS=5
```

## Flags

- `-debug` - enable diagnostic logging (default: false)
- `-debug-transcribe` - enable transcription text logging (requires `-debug`)
- `-format <mp3@16|mp3@64|flac>` - audio format (default: mp3@16)
- `-lang <code>` - language code for transcription (default: en, also settable from tray menu)
- `-device <name>` - use named microphone device (also switchable from tray menu)
- `-setup` - select microphone device interactively
- `-doctor` - run system diagnostics and exit
- `-benchmark <wav>` - run benchmark instead of live recording
- `-runs N` - benchmark iterations (default: 3)
- `-logpath <path>` - log directory (default: `$ZEE_LOG_PATH` or OS-specific, use `./` for current directory)
- `-hints <words>` - comma-separated vocabulary hints (overrides `hints.txt`)
- `-transcribe <file>` - transcribe an audio file (mp3/flac/wav) and exit

**Environment variables:**
- `ZEE_SAVE_LAST_AUDIO=1` - enables "Save Last Recording" tray button (saves audio + metadata to `config/samples/`)

## Architecture

Push-to-talk transcription using Groq Whisper API:

```
Ctrl+Shift+Space keydown → record audio → encode (mode-based) → API call → clipboard
```

**Files:**
- `main.go` - hotkey handling, audio capture, recording logic, panic recovery
- `tray/` - system tray icon, menus (devices, providers, languages, auto-paste), dynamic icons
- `encoder/` - AudioEncoder interface, FLAC, MP3, and Adaptive implementations
- `transcriber/` - STT providers (Groq, OpenAI, Deepgram, Mistral, ElevenLabs) with shared TracedClient for HTTP timing metrics
- `hotkey/` - global hotkey registration (Ctrl+Shift+Space) with platform-specific backends
- `clipboard/` - platform-specific clipboard and paste operations (Cmd+V / Ctrl+V)
- `audio/` - platform-specific audio I/O: capture (malgo on macOS, PulseAudio on Linux) and feedback-tone playback (`PlayStart/PlayEnd/...`); on macOS both share one malgo context + a private device-lifecycle lock
- `doctor/` - system diagnostics (`-doctor` flag)
- `internal/mp3/` - vendored shine-mp3 encoder (with mono fix)
- `device.go` - microphone picker with arrow-key navigation
- `vad.go` - voice activity detection using WebRTC VAD with debounced speech confirmation
- `silence.go` - silence monitoring with warnings, repeat beeps, and auto-close (toggle mode)
- `config/` - persistent settings (`config.json`) and vocabulary hints (`hints.txt`)
- `log.go` - diagnostic logging and panic capture to `diagnostics_log.txt`

## Design Philosophy

- **Unix philosophy packages** - Each subfolder is a self-contained utility that does one thing: `audio/` does audio device I/O (mic capture + feedback-tone playback), `clipboard/` copies and pastes, `transcriber/` talks to STT APIs, `hotkey/` registers global keys. They expose a minimal interface and hide all platform/provider details behind build tags.
- **Root files are pure business logic** - `main.go` and other root files orchestrate the workflow but never import OS-specific APIs or know implementation details of subpackages. When `main.go` calls `clipboard.Paste()`, it doesn't know whether that's pbcopy, xclip, or Win32 — and it shouldn't. Same for `audio.PlayEnd()`, `audio.NewContext()`, `transcriber.Transcribe()`, etc.
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
- `transcribe_log.txt` - transcription text history (requires `-debug -debug-transcribe`)

## Releasing

GoReleaser handles the full release pipeline via `.goreleaser.yml`:

```bash
git tag v0.3.0 && git push origin v0.3.0   # triggers CI release
```

CI (`.github/workflows/release.yml`) does:
1. Verify tag is on `main`
2. GoReleaser builds arm64 + amd64 binaries, universal binary, tar.gz archives, checksums, GitHub release
3. Post-step creates DMG from universal binary, appends its SHA256 to `checksums.txt`, uploads to release

Requires `ZEE_RELEASE_TOKEN` repo secret (fine-grained PAT with Contents read/write on `zee`).

Users install via the one-liner in README (`install.sh` fetches the DMG and verifies the checksum).

### Releasing a new local model (Parakeet GGUF)

Models are hosted in an immutable, never-"latest" `models-vN` GitHub release, **separate** from app releases. `localmodel/localmodel.go` is the single source of truth (filenames, SHA256s, `PreFetch` flags); `localmodel/manifest.txt` is its generated, committed projection that `install.sh` reads from `main`. Nothing model-related is hardcoded in `install.sh`.

1. Produce the `.gguf` locally (parakeet.cpp conversion/quantization) into a folder, e.g. `./out`.
2. Add/edit its entry in `localmodel.go` — `Filename`, `SHA256` (`shasum -a 256`), `SizeBytes` (`ls -l`), `Decoder`, `Multilingual`, `PreFetch`. Re-quantizing existing files → also bump `localmodel.Version` and `install.sh`'s `MODELS_TAG`.
3. `make model-release MODELS_DIR=./out MODELS_TAG=models-vN` — regenerates `manifest.txt`, verifies the ggufs against the registry SHA256s, and uploads them with `--latest=false`.
4. Commit `localmodel.go` + `localmodel/manifest.txt` (+ `install.sh` if `MODELS_TAG` changed). `TestManifestUpToDate` fails the build if the manifest is stale.

`make manifest` regenerates the manifest alone. Dev builds pull prefetch models via `make download-models` (`localmodels download`). The CLI `cmd/localmodels` has exactly two verbs: `download` (dev) and `manifest` (release).

## Packaging

- `packaging/appicon.png` - source icon (1024px, stack Z design: shadow square + framed Z, transparent background)
- `packaging/mkicns.sh` - generates `Zee.icns` from `appicon.png` (via `make icns`)
- `packaging/mkdmg.sh` - creates DMG with Zee.app + Applications symlink (via `make app`)
- `packaging/Info.plist` - app bundle metadata (version templated from git tag)
- `.goreleaser.yml` - GoReleaser config (builds, archives, checksums, Homebrew formula)
- `Zee.icns` and `Zee-*.dmg` are gitignored (derived artifacts)
