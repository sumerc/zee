# Design Notes

Ideas and tradeoffs decided while building zee. Each entry captures *why* a
choice was made, so we don't relitigate it (or silently regress it) later.

Entries record what was **measured**, not what was expected — including options
that were tried and rejected, so a future attempt starts from the evidence
rather than the intuition. When a later measurement supersedes an earlier one,
the old entry is marked superseded rather than deleted: the fact that something
*used to be true* is usually why the code looks the way it does.

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

> **Partly superseded** — Parakeet is still the local *English* default, but the
> multilingual role moved from Parakeet v3 to Whisper in models-v2. The v3/v2
> model claims below are kept as the reasoning that held at the time; see
> "Why Whisper for multilingual" for what replaced them.

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
  **Answered: yes** — whisper.cpp v1.9.1 builds and runs against ggml v0.13.0,
  and both engines coexist in one process over one ggml (next section).

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

## Why the state icons are a bare coloured dot

Idle is the Z glyph as a macOS **template** image (alpha only; AppKit tints it to
match the bar). Each state is a flat saturated dot and nothing else — red
recording, orange hearing-nothing, blue transcribing — generated by
`go run ./packaging/mktrayicons`. A saturated dot is legible on a light and a
dark bar alike, so the states need no light/dark variants and **no code detects
the bar's appearance**.

The constraint everything here follows from: an `NSStatusItem` holds exactly one
image, and that image is either a template (auto-tinted, but alpha-only, so all
colour is discarded) or a plain coloured image (keeps colour, but nothing tints
it for you). "Auto-tinted glyph + coloured badge" is not expressible. Any design
that wants both must re-implement the tinting, which means detecting the bar's
appearance — and that detection is the entire cost.

The bug that started this: the variant used to be picked from
`AppleInterfaceStyle`, the **global Light/Dark toggle**, which is not what the
menu bar follows. On macOS Tahoe the bar is glass — appearance derived from the
wallpaper behind it, per screen, re-tinting live — so light mode + dark wallpaper
drew a dark glyph on a dark bar. The value was also cached once per launch, so
even a correct answer went stale. Idle never broke; it was already a template.

Rejected alternatives:
- **Glyph + coloured badge, variant chosen from the status button's
  `effectiveAppearance`.** Built and reverted. It is the *correct* way to keep
  both (measured on Tahoe: `button.effectiveAppearance` = VibrantLight while
  `NSApp.effectiveAppearance` = Aqua — the button is the only object that knows,
  and it is KVO-able), but it cost ~100 lines of cgo: the systray library keeps
  its `NSStatusItem` private, so the button had to be found by walking the app's
  own window list, plus a KVO observer and a cached answer because AppKit cannot
  be touched from the recording goroutine. Too much machinery to choose between
  two PNGs; dropping the glyph during states deletes all of it.
- **All-template icons, state as badge shape** (disc/ring/bar). Zero detection
  and correct everywhere, but monochrome: at a glance the *colour* is the state
  signal, and the marks are ~5.5 pt, so shape carries much less.
- **Template glyph + `button.contentTintColor`.** Lets the system solve
  light/dark and keeps a colour, but tints the *whole* glyph — a red Z, not a
  red dot — and needs the same private button.

Cost accepted: during a state the menu bar shows only a dot, not the Z. The
icon's position in the bar already identifies the app, and the colour is the
information.

## Why Whisper for multilingual (models-v2)

Parakeet v3 was the offline multilingual model; **whisper large-v3-turbo q5_0**
replaced it. Two models, two roles:

```
+---------------------+----------+---------+--------+--------------------------+
| Model               | Engine   | Size    | Langs  | Role                     |
+---------------------+----------+---------+--------+--------------------------+
| parakeet-110m-en    | parakeet | 267 MB  | en     | startup default, instant |
| whisper-turbo-q5    | whisper  | 547 MB  | ~99    | everything else          |
+---------------------+----------+---------+--------+--------------------------+
```

Both pre-fetched; install payload *shrank* 942 → 814 MB. Dropped:
`parakeet-v3-multi` (superseded) and `parakeet-v2-en-large` (1.4 GB; the 110m
already covers English). Saved selections migrate **by role**, so a multilingual
user is never silently downgraded to English-only (`retiredIDs` in
`localmodel/localmodel.go`). Old builds keep working — they pin `models-v1`,
still published.

`ByID` following `retiredIDs` is right for a user's stale `config.json` and wrong
for code that names a model deliberately: the ID resolves, but to a model on a
*different engine*, so pairing it with a hardcoded provider yields whisper
weights loaded by the parakeet engine. That combination does not fail uniformly —
locally it returns a clean `gguf open failed`, on CI it hung silently for 8m24s
until the test timeout (mechanism never identified; the child printed nothing).
The integration tests therefore reject a retired ID instead of following the
migration, and take the provider from `Model.Engine` rather than a literal
(`localModel` in `test/integration_test.go`). Worth remembering if a mismatched
provider/model pair can reach users some other way — the failure may be a hang,
not an error.

M5 Pro, real saved dictations, warm, best of 3:

```
+----------+---------------+--------------+----------------+
| Audio    | parakeet-110m | whisper (en) | whisper (auto) |
+----------+---------------+--------------+----------------+
| 1.9 s    |     18 ms     |    272 ms    |     535 ms     |
| 9.8 s    |     51 ms     |    295 ms    |     581 ms     |
| 70 s     |    309 ms     |   1088 ms    |    1335 ms     |
+----------+---------------+--------------+----------------+
```

**One ggml, two engines.** whisper.cpp v1.9.1 builds against the *patched* ggml
v0.13.0 that parakeet.cpp vendors; both load in one process, no duplicate
symbols. `make whisper-lib` installs parakeet's ggml to a local prefix and
builds `libwhisper.a` against it (`-DWHISPER_USE_SYSTEM_GGML=ON`). Letting
whisper use its own vendored ggml would be worse than a link error: unpatched
headers against patched archives — a silent struct-layout mismatch. zee never
pins ggml directly, it inherits the pin from parakeet.cpp, so a test asserts the
commit and fails loudly if a parakeet bump moves it.

**Metal tensor cores are free on M5.** ggml auto-detects them
(`has tensor = true`, `MTLGPUFamilyMetal4`) with zero integration work — the
main reason M5 whisper is ~3× M1. GPU tensor path, *not* the ANE.

**Auto-detect is the default, and not as a preference.** whisper's language
setting hard-forces the start-of-transcript token, so a wrong setting *garbles*
rather than mislabels: Turkish audio pinned to English came back as "There is a
travel system in a bootstrap…". Groq's `language=en` is a soft hint and survives
the same audio — the local engine is strictly less forgiving than the cloud path
users know.

What matters is that the token matches the **dominant** language, not that
detection is on: on Turkish audio with embedded English, `-l tr` output was
*identical* to `-l auto`, English terms intact under both. Whisper transcribes
foreign words regardless of the token; only the wrong dominant language breaks
it. Auto is the default because that language isn't known before the user
speaks — the mixed-language case zee exists for. Cost: one extra encoder pass,
~260 ms.

Parakeet is unaffected — no language parameter in its C-API; each gguf is
single-language by build.

**q5_0, not f16** — free on both axes. Warm latency a tie (273 vs 270 ms). No
quality direction: over 5 real dictations (537 words) the models diverged on
4.8% of words; adjudicating each differing span gave q5_0 closer on 5, f16 on 1,
rest a wash. Short clean clips byte-identical; most long-clip divergence is
repetition-loop garble present in *both* (long-audio decoding artifact, not
quantization). So q5_0 wins on size alone: 547 MB vs 1.6 GB.

*Caveat:* that is **divergence between the two models**, not WER — the
references are the models' own output. It proves "quantization changes nothing
measurable here"; it cannot rank either against truth. Real WER needs a labelled
corpus (accented/noisy/non-English, human transcripts) we don't have.

**Metal, not Core ML/ANE — measured, then declined.** The ANE path works
(`-DWHISPER_COREML=1`, `.mlmodelc` encoder bundle, `-framework CoreML`) and is
faster:

```
Audio    Metal-only   +CoreML/ANE   gain
2.9 s      273 ms       217 ms      -21%
11.0 s     295 ms       247 ms      -16%
22.8 s     389 ms       331 ms      -15%
```

Declined on price, not size of gain: **+1.2 GB per model** (a second encoder
copy on top of the 547 MB gguf — more than doubles the payload) and **an ANE
recompile on every app update**, since the specialization is keyed to the bundle
(~4.5 s on M5, ~2.5 min on M1 — a visible freeze on exactly the slower machine
that needs the speed). The gain also shrinks as hardware improves: ~30% on M1 →
~15–20% on M5, as Metal's tensor cores absorbed it. And it buys nothing on the
common path: parakeet is 4–9× faster than *either* whisper config for English.
GGUF/ggml can never reach the ANE (GPU-only), so Core ML is the only route —
this is the whole decision.

*Open:* enabling ANE only on M1-class hardware (e.g. at install time) is an
unresolved trade — ~30% there is worth more than ~15% here, but it means a
second asset pipeline and a 2.5-min post-update compile.

**`audio_ctx` sizing: measured, then rejected.** whisper's encoder always
processes a fixed 1500-frame (30 s) window, so a 2 s clip costs the same encode
as a 30 s one — visible above as whisper's near-flat 272/295 ms across a 5×
range of audio length. Capping the window to the clip is a ~3× short-clip win
and measured clean via `whisper-cli`. Unusable: shrinking `audio_ctx` on a
reused `whisper_state` yields fluent garbage.

```
fresh ctx at ac=400 · same size repeatedly · two clips at one size   correct
200 -> 400 (grow)                                                    correct
400 -> 200, and full/1500 -> 400 (shrink)                            garbage
```

Reproduced byte-identically on pure upstream ggml v0.13.0 → upstream
whisper.cpp bug, not a parakeet ggml patch. (Upstream issue #1855 proposes this
exact scheme; no maintainer validated it.) Two things close it for zee:
dictation lengths vary, so per-clip sizing inevitably shrinks; and auto-detect
shrinks *within* one `whisper_full` call, because the detection encode runs
before `exp_n_audio_ctx` is assigned (v1.9.1, line 6836 vs 6972). At
`audio_ctx = 0` sizes never change and the bug cannot fire. A grow-only scheme,
or fresh `whisper_state` per utterance, could revisit;
`internal/whisper/audioctx_matrix_test.go` (`ZEE_AC_DEBUG=1`) re-validates any
attempt in seconds.

Ruled out while chasing it (each tested, not assumed): sampling strategy (beam
search — whisper-cli's actual default at `beam_size=5` — garbles identically),
`no_timestamps`, `flash_attn`/`use_gpu`, `no_context`, warm-up content, and
audio length varying between calls.

**The POC's exit-134 abort did not follow us.** whisper+parakeet in one process
aborted on exit in the C driver; in zee both engines load and tear down cleanly
(exit 0, repeatedly). That was the driver's teardown order, not ggml sharing.

Raw measurement detail: `zee-whisper-poc/FINDINGS.md`.

**Silence trimming (VAD before inference): not a whisper speed lever.**
Handy runs Silero VAD during capture and drops non-speech before the model
ever sees it — tempting to copy, but the speedup doesn't transfer. whisper's
encoder pads every clip to the fixed 1500-frame/30 s window, so trimming
silence out of a ≤30 s dictation changes the encode cost by exactly nothing.
It pays for Handy because their *default* models are Parakeet/Nemotron/Canary,
which have no fixed window — compute scales with actual audio, every trimmed
second is a saved second. For whisper it only helps at the margins: clips
>30 s (one encoder pass per 30 s window, so trimming can drop a window) and
fewer hallucinated tokens to decode. The real whisper benefit is *quality*,
not speed: silence is what produces the "thank you for watching" class of
hallucinations. Not adopted: zee's clips are mostly speech (push-to-talk),
the mic tail is deliberate (added to stop last-word clipping), and silence
hallucinations haven't shown up in the logs. Revisit only if they do.

**flash_attn: on by default, verified worth ~12% on M5 (2026-07-25).**
whisper.cpp defaults `flash_attn=true` since v1.8.0 and zee passes default
context params, so the win was already banked. A/B with FA forced off
(bench-local, turbo-q5, saved samples, M5 Pro): 262→297 ms (1.9 s clip),
2419→2641 ms (183 s) — ~12% short, ~8% long. Far under the 30–47% upstream
measured on M1–M4: M5's Metal tensor cores absorb most of what FA saves.
Same pattern as Core ML — encoder tricks shrink as hardware improves.

**q8_0 vs q5_0 on M5: a wash, q5_0 stays (2026-07-25).** Community claim was
"q8_0 equal-or-faster on GPU (cheaper dequant), quality ≥". Measured
(bench-local, saved samples, M5 Pro, same run): short clips ~3% faster on q8
(263→256 ms en, 514→498 ms auto), the 183 s clip ~3% *slower* (2429→2504 ms
en) — inside noise both directions. Not worth +300 MB disk (874 vs 574 MB)
and ~+300 MB resident. Quality not re-evaluated: the q5-vs-f16 entry above
already showed quantization deltas are unmeasurable on our corpus, and q8
sits between them. *Open:* same A/B pending on M1 — no tensor cores there,
so q8's cheaper dequant could matter more; decision is per-M5 until then.

**temperature_inc=0: no average-case win, not adopted (2026-07-25).** The
default fallback ladder (up to 5 re-decodes at rising temperature when
compression-ratio/logprob thresholds fail) is a worst-case latency risk, so
disabling it was benchmarked: every clip within noise of baseline — clean
push-to-talk audio never trips the thresholds, so the ladder never runs.
Kept enabled because it only costs when it fires, and when it fires it is
rescuing quality on hard audio. Revisit if diagnostics ever show an
`inference_ms` far above its `audio_s` peers (the stall signature).
