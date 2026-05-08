---
name: wer-wolf
description: Benchmark Zee's saved audio samples against every available STT provider/model (named for Word Error Rate — the canonical STT eval metric). Use when the user wants to compare transcription quality across Groq Whisper, OpenAI, Mistral Voxtral, ElevenLabs Scribe, etc., on their own saved recordings, evaluate which model handles their domain vocabulary best, or audit how well hints.txt biasing works per provider.
---

# wer-wolf — STT bake-off for Zee samples

Run every saved sample under `~/Library/Application Support/zee/samples/` through every STT provider Zee supports (that has an API key) and present a side-by-side comparison.

## What this does

1. Lists every sample directory under `~/Library/Application Support/zee/samples/`.
2. For each sample, reads `info.json` (original provider/model, original transcribed text, timestamp) and stats `audio.<ext>` for KB size + format.
3. Loops over `(provider, model)` pairs whose API key env var is set, swapping `config.json` for each, calling `./zee -transcribe <audio_file>`. Hints are read automatically from `hints.txt` by all five providers.
4. Restores the original `config.json` at the end (also on error — script uses a trap).
5. Renders the result as one block per sample: metadata header followed by an ASCII table of every model's output.

## Pre-flight checks

Before running, verify:

- **Zee binary**. Find the running process first (most accurate, matches what the user is actually using):
  ```bash
  lsof -c zee 2>/dev/null | awk '$4=="txt" && $NF ~ /zee$/ {print $NF; exit}'
  ```
  Fall back to `/Users/supo/Desktop/p/zee/zee` if no process. If neither exists, ask the user where the binary is.
- **Samples directory** exists and has at least one `2026-*` subdir. If empty, tell the user to enable `ZEE_SAVE_LAST_AUDIO=1` and capture some recordings first.
- **API keys**. Print which of `GROQ_API_KEY`, `OPENAI_API_KEY`, `MISTRAL_API_KEY`, `ELEVENLABS_API_KEY` are set; the script auto-skips providers whose key is missing. Deepgram is skipped entirely (its only model `nova-3` is streaming, not compatible with batch `-transcribe`).
- **Tray app warning**. Tell the user not to interact with the running tray app's menu during the run — it caches `config.json` in memory and will overwrite the file on the next menu interaction. Backup is restored at the end either way, but mid-run interference can corrupt results.

## Running it

```bash
ZEE_BIN=<path> bash ~/.claude/skills/zee-transcribe-compare/scripts/compare.sh
```

The script:
- Writes raw results to `/tmp/zee-compare-results.txt`.
- Writes machine-readable per-sample/per-model JSON lines to `/tmp/zee-compare-results.jsonl` (each line: `{sample, provider, model, text, error?}`). Use this for the comparison table — easier than re-parsing the human-readable file.

## How to render the result

Start with a header showing the active vocabulary so the user can see what biasing the providers received:

```
**Hints in effect** (`~/Library/Application Support/zee/hints.txt`):
<comma-joined non-comment, non-empty lines>
```

Then for each sample directory (sorted by timestamp), produce one block:

```
### <sample-id>  —  <KB> KB <format>  —  recorded <timestamp>
**Originally transcribed by:** <provider> / <model>
**Original text:** "<text>"

| Provider / Model         | Latency  | Transcription                  |
|--------------------------|----------|--------------------------------|
| groq / whisper-v3-turbo  | 412 ms   | ...                            |
| ...                      | ...      | ...                            |
```

Mark the row matching the original `(provider, model)` with `*` after the model name so the user can quickly see the baseline.

If a model errored (network, 4xx, etc.), put the error message in the cell instead of the transcription.

## Models tested per provider

(Source of truth: `transcriber/*.go` in the zee repo. Update this list if Zee adds models.)

| Provider     | Model(s)                                   | Hint field              |
|--------------|--------------------------------------------|-------------------------|
| groq         | `whisper-large-v3-turbo`, `whisper-large-v3` | `prompt`              |
| openai       | `gpt-4o-transcribe`                         | `prompt`               |
| mistral      | `voxtral-mini-latest`                       | `context_bias[]`       |
| elevenlabs   | `scribe_v2`                                 | `keyterms[]`           |
| deepgram     | `nova-3` (streaming-only — skipped)         | streaming keyterms     |

All five wire `hints.txt` automatically — no flag required. Each provider receives the same hints joined as a single comma-separated string; how aggressively each provider biases varies a lot in practice (Whisper-via-Groq honors it strongly; Mistral and Scribe much less so based on observed runs).

## Notes / gotchas

- Each `-transcribe` call is one HTTP round-trip (~1–3s). 4 samples × 4 models ≈ 12–20s wall time plus API latency. Costs are tiny but real.
- `voxtral-mini-latest` is a moving target — if Mistral renames it, update the script.
- The script intentionally does **not** parallelize providers — keeps output ordered and avoids rate-limit surprises.
- `-transcribe` exits non-zero on transcription errors; the script captures stdout+stderr and continues so one failure doesn't abort the matrix.
