# Reference

Everything that doesn't belong in the [README](../README.md): the full flag
surface, environment variables, building from source, and testing.

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
| `-hints` | – | Vocabulary hints, comma-separated (overrides `hints.txt`; `-hints ""` = none) |
| `-no-hints` | `false` | Ignore `hints.txt` and send no hints; cannot be combined with a non-empty `-hints` |
| `-transcribe` | – | Transcribe audio file(s) and exit; extra files may follow as positional args, one transcript per line |
| `-setup` | `false` | Same as `zee setup` |
| `-debug-transcribe` | `false` | Log transcription text and save each clip's audio + transcript under `recordings/` (diagnostics are always logged) |
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
| `config.json` | Settings: provider, model, device, hotkey, language, auto-paste, post-transcribe |
| `credentials.json` | Per-provider API keys, mode 0600. Environment variables are *not* read |
| `hints.txt` | Vocabulary hints fed to the model |
| `samples/` | Recordings saved from the tray, plus auto-saved failures |

### Post-transcribe

An optional LLM pass rewrites each transcript before it is pasted. It is off by
default. Pick a profile from the tray (**Settings → Post-Transcribe**, **None**
turns it off) or set it in `config.json` and **Reload Config**; either way it
applies from the next recording, with no restart. It uses the `groq` key from
`credentials.json`, with any provider for the transcription itself.

```json
"post_transcribe": {
  "active": "friendly",
  "model": "openai/gpt-oss-120b",
  "prompts": {
    "slack": "Rewrite it as a short, casual Slack message. Fix all grammatical errors."
  }
}
```

- `active` — the profile to run; `""` (the default) turns it off.
- `prompts` — your own profiles, name → a one- or two-sentence instruction. A
  name that matches a built-in one replaces it.
- `model` — any Groq chat model; omit it for `openai/gpt-oss-120b`.
- `colors` — each profile's colour for the recording overlay, `"#RRGGBB"`. zee
  gives a profile without one a random colour from its palette and saves it,
  so the colour stays put; edit it to choose your own.

Built-in profiles: `grammar` (spelling, punctuation, grammar; wording kept),
`friendly` (conversational tone), `professional` (work email tone), `concise`
(filler and repetition removed). Every profile also corrects misheard words
toward `hints.txt`, keeps the sentence in the language you spoke it in (English
words inside a Turkish sentence stay English), and never answers the dictated
text.

While a profile is active, the recording overlay shows its name in the
top-right corner, in its colour.

If the call fails or times out, the raw transcript is pasted and the tray shows
the error. In streaming mode the text is pasted once at the end instead of
while you speak. With `-debug-transcribe`, each recording folder keeps the raw
`transcript.txt` beside the rewritten `post_transcript.txt`, and `info.json`
records the profile, the model and any error. The rewrite's time is
`post_transcribe_ms` in the `felt_latency` line.

Logs live in `~/Library/Logs/zee/`: `diagnostics_log.txt` (timing, errors;
rotated at 10 MB), `crash_log.txt` (panics), and `transcribe_log.txt` (only with
`-debug-transcribe`; each line points to its `recordings/<timestamp>/` folder of
`audio.<ext>`, `transcript.txt`, `info.json` and, when post-transcribe rewrote
it, `post_transcript.txt`; the folder is never pruned).

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
