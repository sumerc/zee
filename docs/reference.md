# Reference

Everything that doesn't belong in the [README](../README.md): the full flag
surface, environment variables, building from source, and testing.

For *why* the non-obvious choices were made — engines, models, backends, what was
measured and what was rejected — see [design-notes.md](design-notes.md).

## Subcommands

| Command | What it does |
|---|---|
| `zee setup` | Interactive wizard: microphone + live transcription test, hotkey capture + fire test, permissions, cloud providers (each API key live-tested) |
| `zee doctor` | Zero-question health check against your saved config: hold the hotkey, speak, release. Exit code reflects health |
| `zee update` | Download + verify the latest release, swap it into place, then re-run setup (macOS drops permissions when the bundle changes) |

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-format` | `mp3@16` | Audio format: `mp3@16`, `mp3@64`, or `flac` |
| `-lang` | `en` | Language code (`en`, `tr`, `es`, …); empty = auto-detect |
| `-device` | (system default) | Use named microphone device |
| `-provider` | (saved config) | Transcription provider (`parakeet`, `whisper`, `groq`, …) |
| `-model` | (saved config) | Model ID for the selected provider |
| `-autopaste` | `true` | Auto-paste into the focused window |
| `-hints` | – | Vocabulary hints, comma-separated (overrides `hints.txt`) |
| `-transcribe` | – | Transcribe audio file(s) and exit; extra files may follow as positional args, one transcript per line |
| `-setup` | `false` | Same as `zee setup` |
| `-debug-transcribe` | `false` | Log transcription text (diagnostics are always logged) |
| `-logpath` | OS-specific | Log directory (`./` for current dir) |
| `-benchmark` | – | WAV file to benchmark instead of live recording |
| `-runs` | `3` | Benchmark iterations |
| `-version` | `false` | Print version and exit |

## Environment

| Variable | Description |
|----------|-------------|
| `ZEE_CONFIG_DIR` | Override the config directory entirely |
| `ZEE_LOG_PATH` | Log directory override |
| `ZEE_LONGPRESS_DURATION` | Push-to-talk vs tap-to-toggle threshold (e.g. `350ms`) |
| `ZEE_PPROF` | pprof server address (e.g. `:6060`) |
| `ZEE_CRASH=1` | Trigger a synthetic crash, for testing the crash log |

## Files

Config lives in `~/Library/Application Support/zee/` (a dev build keeps its own
`.zee/` next to the binary, so a working copy never collides with the installed
app):

| File | Contents |
|---|---|
| `config.json` | Settings: provider, model, device, hotkey, language, auto-paste |
| `credentials.json` | Per-provider API keys, mode 0600. Environment variables are *not* read |
| `hints.txt` | Vocabulary hints fed to the model |
| `samples/` | Recordings saved from the tray, plus auto-saved failures |

Logs live in `~/Library/Logs/zee/`: `diagnostics_log.txt` (timing, errors;
rotated at 10 MB), `crash_log.txt` (panics), and `transcribe_log.txt` (only with
`-debug-transcribe`).

## Other ways to install

CLI binary, for terminal use:

```bash
curl -L https://github.com/sumerc/zee/releases/latest/download/zee_darwin_arm64.tar.gz | tar xz
./zee
```

> When run from a terminal, macOS attributes Microphone and Accessibility to the
> **terminal app** (Ghostty, iTerm2, Terminal), not to zee.

Or grab `Zee-<version>.dmg` from the
[latest release](https://github.com/sumerc/zee/releases/latest), drag **Zee.app**
to **Applications**, then `xattr -cr /Applications/Zee.app`.

## Build from source

Requires Apple Silicon, `cmake`, and the Xcode Command Line Tools — the on-device
STT engines (parakeet.cpp + whisper.cpp) are built once locally.

```bash
git clone https://github.com/sumerc/zee && cd zee
make build        # engines (cmake) + binary; first run fetches the default models (~800 MB)
make app          # macOS DMG
```

The submodule, static libraries, and models are all handled by `make build`.

## Testing

```bash
make test                             # unit tests
make test-integration                 # end-to-end (requires GROQ_API_KEY)
make benchmark WAV=file.wav RUNS=5    # whole pipeline: encode + provider + network
make bench-local                      # local inference only, no network
make bench-save                       # append a labelled baseline to benchmark.txt
```

`make bench-local` isolates on-device inference (model load + `Transcribe`) so
engine bumps and quantization changes are A/B-comparable. It reports `ns/op` and
`xRT` (audio seconds per wall second — higher is faster) per `<model>/<clip>`.

`WAV=` takes a file **or** a directory, so you can benchmark your own voice
against the samples you've saved:

```bash
make bench-local WAV="$HOME/Library/Application Support/zee/samples" RUNS=5
```

Clips must be 16 kHz mono 16-bit; anything else is skipped rather than
benchmarked wrong.
