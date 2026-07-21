# Design Notes

Ideas and tradeoffs decided while building zee. Each entry captures *why* a
choice was made, so we don't relitigate it (or silently regress it) later.

## Why the audio device is re-init'd on every recording

`audio.Start()` tears down the capture device (`Uninit`) and rebuilds it
(`InitDevice`) on **every** recording, instead of initializing it once at
startup and reusing it.

**Reason:** after macOS sleep/wake (and some device/route changes) the malgo
(miniaudio/CoreAudio) device handle goes **stale silently** — `device.Start()`
returns no error, but the mic produces only silence. There's no reliable signal
to detect this, so the blunt-but-safe fix is to rebuild the device every time.
This replaced an earlier "start, and only recreate on error" approach that
didn't work because the stale handle never reported an error.

**Tradeoff:** constant `Uninit`/`InitDevice` churn enlarges the surface for
miniaudio lifecycle bugs. It directly caused a double-free crash: if the rebuild
failed transiently, the old (already-freed) device pointer was retained and the
next `Start` uninited it a second time (SIGABRT/SIGSEGV in `ma_device_uninit`).
Fixed by never keeping a freed pointer — store `nil` if the rebuild fails (see
`audio/capture_darwin.go`). The same teardown-on-use pattern (and the same fix)
applies to `beep` playback (`audio/beep_darwin.go`).

**Better long-term options (not yet done):**
- Init once + reinit only on a real signal — macOS `NSWorkspace` sleep/wake
  notification, or miniaudio's reroute/stop notification callback. Most correct,
  but only manually verifiable (`pmset sleepnow`, wake, record).
- Init once + stale-by-behavior detection — keep the device, and after `Start`
  watch the data callback for actual frames; reinit only if none arrive. Catches
  every stale cause, and is unit-testable with a fake device, but it's heuristic.

All miniaudio device lifecycle calls (capture + playback) are also serialized
behind a process-wide lock (`deviceMu` in `audio/capture_darwin.go`) as defense
against concurrent init/uninit across the two malgo contexts.

## Why Parakeet (local default), CPU, and which model

zee runs NVIDIA NeMo **Parakeet** locally (via `parakeet.cpp`/`libparakeet`/ggml)
as the default engine, with cloud providers (Groq, OpenAI, …) as accuracy/noise
fallbacks. Local-first wins on privacy and latency; the cloud path is there for
when accuracy matters or the environment is noisy. All numbers below were
measured on an M1 Pro.

**CPU backend, not Metal** — *but see below, fixed in mudler v0.4.0.* ggml's
Metal backend doesn't implement `CONV_2D_DW`, the depthwise conv the
FastConformer encoder needs, and Parakeet ran the whole graph on one backend
with no per-op CPU fallback — so a Metal build *aborted*, it didn't degrade. The
CPU path runs at 20–65x real-time, which is plenty for dictation-length clips;
Metal would only matter for batching hours of audio. (A different Metal-capable
backend exists but only beats CPU on the 0.6b model and *loses* on our 110m
default.)

**Metal works as of mudler v0.4.0 (bump from v0.1.1+4).** The abort above is
superseded — *not* by ggml (still v0.13.0, still missing `CONV_2D_DW`) but by
parakeet's own fix (upstream #2 / PR #4): a `ggml_backend_sched` over `{GPU,CPU}`
that offloads unsupported ops to CPU, plus a native Metal depthwise-conv kernel.
Same ggml pin, so no ggml movement. Both 110m and the v3 multilingual GGUF (the
model that used to crash) transcribe cleanly on Metal.

- **Speedup** (synthetic TTS, wall-clock A/B, same clips both sides — read as
  relative, not absolute): steady-state RTFx 110m 22→**38x** (~1.7x), v3-0.6b
  8→**21x** (~2.6x). Per-utterance warm-daemon on a 10s clip: 110m 0.45→0.26 s,
  v3 1.25→**0.48 s**. The win scales with model size, so it's **marginal for the
  English 110m default** (both already sub-perceptible; fixed overhead dominates)
  but **real for multilingual** (v3/Turkish) — the case that justifies Metal.
- **Cold-start is paid once, not per utterance.** Shaders are embedded in
  `libggml-metal.a` (no shipped `.metallib` — single-binary intact) and the OS
  caches compiled pipelines, so the two-point fit put fixed overhead at ~0–0.3 s.
  In the daemon it lands once at launch: bake a warmup `Transcribe` on a synthetic
  buffer into startup (same spirit as HTTP pre-warming), sized near dictation
  length so the common tensor shapes pre-compile.
- **Whisper angle:** since Metal now works on the *shared* ggml v0.13.0, a future
  whisper.cpp integration could link the same Metal-enabled ggml — and whisper
  benefits from GPU more (larger models, offline ~99-language incl. Turkish).
  Still gated on the open POC: does whisper.cpp v1.9.1 build against ggml v0.13.0
  (see `zee-whisper-poc/PLAN.md`).

**Static-linked** (`BUILD_SHARED_LIBS=OFF` → `.a` folded into the Go binary):
one self-contained arm64 binary, no shipped `lib/`, no dylib version-skew. Cost
is ~4 MB and a relink when the C side changes — worth it for a drop-in app.

**Model defaults (measured):**

```
+-------------------+-------+--------+----------+--------+------------------------+
| Use               | Model | Size   | Load     | RTFx   | Felt latency (warm)    |
+-------------------+-------+--------+----------+--------+------------------------+
| English (default) | 110m  | 255 MB | ~55 ms   | ~36x   | 10s clip → ~150-300 ms |
| Multilingual      | v3-   | 1.44GB | ~2.4 s   | ~10x   | 10s clip → ~0.7-1.6 s  |
|  (25 languages)   | 0.6b  |        |          |        |                        |
+-------------------+-------+--------+----------+--------+------------------------+
```

- **Both use the TDT head → one decode path, just swap the `.gguf`.** TDT's
  implicit LM wins short/dictation clips (CTC is steadier on long-form, so
  reconsider CTC if we add a meetings mode).
- **110m ties the 0.6b-v2 on accuracy** on our 9-sample corpus (~14% WER, within
  noise) at **1/5 the size and 2.5x the speed** — the 0.6b buys nothing for
  English. (Handy/VoiceInk ship the 0.6b; revisit only if a larger eval shows a
  real gap.)
- **Keep the multilingual model warm** — its ~2.4 s load must never land
  per-utterance. Load once at startup, transcribe per clip.
- **Don't quantize for speed.** q4_k is ~26% *slower* on CPU (per-matmul dequant
  vs the f16 Accelerate/AMX path); it's a footprint/load-time win only.

**What we tested (so read the WER as relative, not absolute):** a 9-sample
corpus, 539 reference words — a mix of dictation-length clips plus one 350-word
(~3-min) clip that alone is 65% of the words; the RTFx/leaderboard figures come
from a single 184 s clip (and the `-mcpu` tuning bench from a 33.8 s clip).
References are cloud-transcription-derived, not human gold, so WER is a relative
ranking signal (±noise), not ground truth. "Tied on accuracy" means within that
noise; TDT's dictation edge is 4–2 on the *short* clips, while the lone long clip
(which CTC won) dominates the corpus average.

**"Instant Parakeet" is datacenter RTFx, not laptop.** The 1500–3400x figures are
A100/H100 batched runs (a 3-min clip in tens of ms). On M-series CPU the encoder
is the bottleneck and even a working Metal build couldn't reach ms (the TDT
decoder is autoregressive, latency-bound). Honest local target: English is
perceptually instant (fixed overhead dominates), multilingual scales ~linearly.

**The biggest unused lever is VAD trimming** (drop silence before inference —
encoder cost scales with frames). Not done because it would let a VAD *false
negative silently drop real speech*; zee only uses VAD for non-critical "still
recording" UI, never to gate what reaches the model. Safe partial win: VAD for
auto-stop only, still send the full buffer.

Full benchmarks, decoder/threading/deployment-target details, and the
provider-WER comparison live in internal notes (`mynotes/notes/local-stt-models.md`,
`stt-model-comparisons.md`); the `wer-wolf` skill is the repeatable way to re-run
provider/model evals on saved samples.
