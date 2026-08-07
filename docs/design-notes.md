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

> **Partly superseded** — Parakeet is still the local *English* default. The
> multilingual role moved from Parakeet v3 to Whisper in models-v2, then split:
> v3 is back as the opt-in *fast* multilingual model and Whisper is the coverage
> default. The v2 claims below are kept as the reasoning that held at the time;
> see "Why Whisper for multilingual" and "Parakeet v3 back as the fast
> multilingual option" for what replaced them. **Which Parakeet serves English is
> also reopened** (2026-08-06): the 110m-vs-0.6b-v2 verdict below was measured on
> CPU and does not survive Metal — see "Parakeet 0.6b-v2 beats the 110m on
> English". Everything here about *CPU vs Metal* and *quantization on CPU* still
> describes why the code looks the way it does.

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
  but **real for multilingual** (v3) — the case that justifies Metal. (This
  entry originally wrote that as "v3/Turkish"; wrong — Turkish is not among v3's
  25 languages. The Metal win on v3 is real, the Turkish framing never was.)
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
  real gap.) **Superseded 2026-08-06** — the larger eval showed the gap, and the
  "2.5x the speed" half was a CPU-era number that Metal erased. See "Parakeet
  0.6b-v2 beats the 110m on English".
- **Keep the multilingual model warm** — its ~2.4 s load must never land
  per-utterance. Load once at startup, transcribe per clip.
- **Don't quantize for speed.** q4_k is ~26% *slower* on CPU (per-matmul dequant
  vs the f16 Accelerate/AMX path); it's a footprint/load-time win only. **Scoped
  to CPU** — on the Metal backend the dequant penalty is gone, which is why
  v3-q4_k is now both the smaller *and* the fast multilingual model (see
  "Parakeet v3 back as the fast multilingual option").

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

> **Partly superseded** — the "Whisper strictly replaces v3" premise below did
> not survive measurement. Whisper is still the multilingual *default* and the
> coverage engine, but v3 came back as an opt-in fast path; see "Parakeet v3 back
> as the fast multilingual option". Everything else here (auto-detect, q5_0, the
> one-ggml-two-engines build, the `retiredIDs` hazard) still holds.

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
already covers English — **that premise fell 2026-08-06**, see "Parakeet 0.6b-v2
beats the 110m on English"; the file is still published under `models-v1`). Saved selections migrate **by role**, so a multilingual
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

M1 Pro (10 cores), same models, warm, best of 3, synthetic clips (2026-07-26).
The floor machine — every number users feel is worst here:

```
+----------+---------------+--------------+----------------+
| Audio    | parakeet-110m | whisper (en) | whisper (auto) |
+----------+---------------+--------------+----------------+
| 2.5 s    |     44 ms     |    947 ms    |    1946 ms     |
| 11.5 s   |    116 ms     |   1049 ms    |    1984 ms     |
| 27 s     |    294 ms     |   1161 ms    |    2164 ms     |
| 60 s     |    761 ms     |   3235 ms    |    4071 ms     |
+----------+---------------+--------------+----------------+
```

Three things this pins down. **whisper is ~3.3× slower than M5** (947 vs 272 ms,
1161 vs ~389 ms) — the ratio claimed above, now with an M1 block in
`benchmark.txt` behind it. **Cost tracks 30 s windows, not audio length:**
947→1049→1161 ms across a 10× range, then a step to 3235 ms at 60 s when a
second window opens. Budget ~1.1–1.6 s per 30 s window, not per second of
speech. **parakeet scales linearly instead** (44/116/294/761 ms), so the gap
widens as clips shorten: 21× at 2.5 s, 4× at 27 s — the routing default is most
valuable exactly where dictation actually lives.

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
speaks — the mixed-language case zee exists for. Cost: one extra encoder pass —
~260 ms on M5, but **~1.0 s on M1 Pro**, where it doubles short-utterance
latency (0.95 s → 1.95 s). It is a full encoder pass, so it scales with the
machine, not the clip: measured +999/+935/+1003 ms across 2.5/11.5/27 s. On
M1-class hardware auto-detect, not whisper itself, is the dominant cost of the
multilingual path.

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
faster. Measured on both machines — the gain is a function of how weak the GPU
is, so read the two tables together, not the average.

M5 Pro:

```
Audio    Metal-only   +CoreML/ANE   gain
2.9 s      273 ms       217 ms      -21%
11.0 s     295 ms       247 ms      -16%
22.8 s     389 ms       331 ms      -15%
```

M1 Pro (in-process, warm, best of 3, `-l en`):

```
Audio    Metal-only   +CoreML/ANE   gain
2.5 s      906 ms       634 ms      -30%
11.5 s     984 ms       721 ms      -27%
27.0 s    1199 ms       879 ms      -27%
```

Encoder alone on M1: ~880–920 ms → ~630 ms (~1.4×), far short of the ~3×
upstream advertises — that figure comes from later ANE generations.

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

**Re-verified 2026-07-26**, after `no_timestamps` was turned off, in case the
decoder-loop fix had also cleared this: the matrix is unchanged (D/F/G/H/I/J/L
garble, A/B/C/E/K pass). Different layer — that fix is in the decoder loop,
this fault is in the encoder state. Nor is it language-specific: `lang=en`
shrinks garble exactly as auto does (0→400, 400→200, 1500→400 all fail with a
fixed language). Auto-detect is only special in that it makes the shrink
*unavoidable* — case H garbles on a fresh context, one call, no prior encode.
Upstream has not fixed it either: v1.9.1 is still the newest tag and none of
the 154 commits since it touch `exp_n_audio_ctx`.

**Partly superseded 2026-07-31.** The fault itself is unchanged — the full
matrix re-ran identical (D/F/G/H/I/J/L garble, A/B/C/E/K pass). What was wrong
is the conclusion drawn from auto-detect. "Auto-detect makes the shrink
unavoidable" holds only on a **cold** state: there `exp_n_audio_ctx` is 0, which
every read site expands to `hparams.n_audio_ctx` (1500), so the first sized call
is a shrink from the full window. It is not a property of auto-detect. The
detect encode at :6836 reads whatever the *previous* call left behind, so if the
state is primed once at a small size with a forced language, later auto calls at
a **larger** size are grows, and grows are safe. Three cases added to the matrix:

```
en@200  -> auto@400   (grow, auto)    correct
en@200  -> auto@200   (same, auto)    correct
auto@400 -> auto@400  (cold, auto)    garbage, and stays garbled
```

So a grow-only scheme does not require forcing a language, which was the main
reason it looked unattractive. Two further facts, measured the same day
(`internal/whisper/audioctx_bench_test.go`, M5 Pro, primed cold context per
size, best of 3 auto calls):

- The win is real and linear in `audio_ctx` — 77 / 132 / 263 / 513 ms at
  200 / 400 / 800 / 1500 on `en.wav`, i.e. ~6.6× at the smallest window.
- **But it needs a floor.** `short.wav` hallucinated at ac=200 and cost
  1113 ms at ac=400 — four times ac=800's 275 ms — because the
  temperature-fallback ladder re-decoded what a too-tight window had broken.
  An over-tight window is both wrong and slower than not sizing at all.
  Upstream #1855's formula picks ~208 for that clip, straight into the failure.
  At ac=800 all four test clips are correct at ~265 ms vs ~511 ms: 1.9×.

Still rejected in the shipped code (`audioCtxFor` returns 0), because the
remaining cost is not obviously worth ~245 ms: the ratchet is sticky — 800
frames is ~16 s of audio, so one long dictation pins the mark at 1500 for the
session — and un-sticking it means a fresh `whisper_state` per utterance, whose
`whisper_init_state` (`src/whisper.cpp:3374`) starts with a full
`whisper_backend_init`, i.e. the Metal setup `whisper.New`'s warm-up exists to
hide. That cost is unmeasured. Note also that sizing and `temperature_inc = 0`
are now known to conflict: the ladder is what repairs the decodes a tight window
breaks. Working notes and the full tables are in `whisper-optimize.md` item 1.


Ruled out while chasing it (each tested, not assumed): sampling strategy (beam
search — whisper-cli's actual default at `beam_size=5` — garbles identically),
`no_timestamps` (not a cause *here* — but a serious bug in its own right, see
"Why whisper runs *with* timestamps" below), `flash_attn`/`use_gpu`,
`no_context`, warm-up content, and audio length varying between calls.

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
second is a saved second. The expectation was that it still helps at the margins on
clips >30 s — one encoder pass per window, so trimming can drop a window.

**Measured 2026-07-26, and the margin win is not there either.** whisper.cpp
v1.9 does the trimming itself (`params.vad` + an 864 KB Silero ggml model): it
detects speech, rebuilds the buffer with speech only, and decodes that
(`whisper.cpp:7779`) — no capture-side work needed. Best of 3, timestamps on,
turbo-q5, M5 Pro:

```
+--------------------------+--------+--------+--------+-------------+
| Clip                     | no-VAD | VAD    | Delta  | Speech kept |
+--------------------------+--------+--------+--------+-------------+
| 159.9 s (6 windows -> 5) | 4.10 s | 4.60 s | +12%   | 81%         |
| 130.9 s (5 windows -> 4) | 4.09 s | 4.09 s |   0%   | 74%         |
|  26.6 s                  | 1.58 s | 1.58 s |   0%   | 74%         |
| 26.6 s + 30 s dead air   | 2.08 s | 2.08 s |   0%   | ~47%        |
+--------------------------+--------+--------+--------+-------------+
```

Dropping a whole window saves nothing: whisper's cost tracks *tokens decoded*,
not windows encoded — a silent window emits EOT almost immediately and is
nearly free, while the words spoken are identical either way. Silero's own pass
then adds cost, which is why the longest clip got slower. Quality was unchanged
too (1762 vs 1792 chars on the 131 s clip, same content, tail intact both ways),
so the predicted hallucination benefit did not appear on this corpus.

Not adopted: zee's clips are mostly speech (push-to-talk), the mic tail is
deliberate (added to stop last-word clipping), silence hallucinations haven't
shown up in the logs, and enabling it would mean hosting, checksumming and
versioning a third model file for a 0% win. Revisit only if silence
hallucinations appear.

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

## How the Core ML/ANE path actually works (mechanism, source-verified 2026-08-04)

Reference for the entry above ("Metal, not Core ML/ANE"). Recorded because the
shape of this path is counter-intuitive and the decision only makes sense once
you see it. Verified by reading `third_party/whisper.cpp` at v1.9.1 (`f049fff9`)
and inspecting the PoC bundle in `~/Desktop/p/personal/zee-whisper-poc/models`.

**The ANE runs the encoder only. It never runs the model.** `whisper_encode_
external()` (`src/whisper.cpp:1958`) returns true when a Core ML context is
loaded; the mel-conv and encoder ggml graphs are then skipped entirely and
`whisper_coreml_encode()` writes straight into `wstate.embd_enc`
(`src/whisper.cpp:2412`). Everything after that — the autoregressive decoder,
KV caches, sampling — stays on ggml/Metal, untouched. Split:

```
audio → mel → [conv + ENCODER] → ANE, Core ML, fp16, .mlmodelc, one pass
                    ↓ embd_enc
              [DECODER loop]   → ggml/Metal + CPU, q5_0, .gguf
                    ↓
                  text
```

**Why only the encoder, structurally.** The encoder is a single pass over a
fixed 1500-frame window — static shapes, big matmuls, exactly what the ANE
compiler wants. The decoder runs once per token with a growing KV cache and
variable length; Core ML can't express that usefully. Upstream converted it
once and shelved it: `models/convert-whisper-to-coreml.py` still carries
`convert_decoder()`, but `generate-coreml-model.sh` hardcodes
`--encoder-only True` and ends with `# TODO: decoder (sometime in the future
maybe)`. This is a property of the ANE, not a whisper.cpp gap — the same
constraint is why FluidAudio/VoiceInk can put *Parakeet* on the ANE (small,
static, CTC/TDT head) but nobody ships an ANE Whisper decoder.

**So you download both files, and the second one is additive.** The full gguf
is always loaded (it holds the decoder); the mlmodelc is loaded *on top*
(`src/whisper.cpp:3440`). Hence "+1.2 GB per model", not "1.2 GB instead of
547 MB".

**gguf quantization can never reach the ANE.** whisper derives the bundle path
from the model path and explicitly strips a `-qX_X` suffix
(`whisper_get_coreml_path_encoder`, `src/whisper.cpp:3326`): `ggml-large-v3-
turbo-q5_0.bin` → `ggml-large-v3-turbo-encoder.mlmodelc`. One encoder bundle
serves every quantization of a model family, by design. There is therefore no
"quantized model on the ANE" configuration to test — the ANE measurements in
the entry above were *already* q5_0-gguf + fp16-ANE-encoder, which is the only
shape this path has.

**What the bundle is, measured.** `weight.bin` = 1,273,969,152 B ≈ 635 M params
× 2 B → fp16. `metadata.json`: `storagePrecision: Float16`, `computePrecision:
Mixed (Float16, Float32, Int32)`. Note the converter's `--quantize` flag means
*fp32 → fp16*, not int8 (`convert-whisper-to-coreml.py:303`, default False, and
`generate-coreml-model.sh` never passes it) — our 1.27 GB bundle is fp16 because
it came prebuilt from HuggingFace, not from that script. A leftover empty
`ggml-large-v3-turbo-q5_0-encoder.mlmodelc/` in the PoC is the dead end from
before the strip logic was understood.

**The one untested variant, and why it is not the win it sounds like.** The
mlmodelc has its own compression track — `coremltools.optimize.coreml`
(palettization, int8 linear) — never tried here. It would attack payload
(1.27 GB → maybe ~350 MB) but not latency: ANE compute is fp16 regardless, so
compressed weights are expanded before the math. It also leaves both real
blockers intact — the per-update ANE recompile (~2.5 min on M1) and a second
asset pipeline. Worth trying only inside the still-open "ANE on M1-class
hardware only" idea, where it makes the payload tolerable.

**Corollary: ANE and `audio_ctx` sizing compete for the same win.** Both
accelerate the encoder and nothing else — but Core ML fixes the encoder input
at 1500 frames, so they are mutually exclusive, and sizing measured ~6× on the
encoder against ANE's ~1.2×, with no second file and no recompile.


## Parakeet v3 back as the fast multilingual option (2026-07-26)

models-v2 retired `parakeet-v3-multi` on the premise that Whisper *strictly*
replaced it. That premise was never benchmarked head-to-head on M1 — the v3
numbers in `benchmark.txt` were M5 only, and the M1 block above has Parakeet 110m
and Whisper columns but no v3. Measured, the premise is wrong: v3 is **3–25×
faster than Whisper** and only ~1.8× slower than the 110m English default. So v3
is back, as an **opt-in download** (643 MB, `PreFetch: false` — install payload
unchanged), and the registry is now three models in three *roles*:

```
+-------------------+----------+--------+-------+----------+-------------------------+
| Model             | Engine   | Size   | Langs | Fetch    | Role                    |
+-------------------+----------+--------+-------+----------+-------------------------+
| parakeet-110m-en  | parakeet | 267 MB | en    | pre      | startup default, instant|
| parakeet-v3-multi | parakeet | 643 MB | 25    | opt-in   | multilingual, fast      |
| whisper-turbo-q5  | whisper  | 547 MB | ~99   | pre      | multilingual, coverage  |
+-------------------+----------+--------+-------+----------+-------------------------+
```

M1 Pro (10 cores), Metal, warm, best of 3, synthetic clips at the same durations
as the M1 block above so the two are comparable. Whisper reproduced that block
within noise (892 vs 947 ms at 2.5 s; 2151 vs 2164 ms auto at 27 s), which is
what makes the v3 column trustworthy:

```
+--------+---------------+-------------+--------------+----------------+
| Audio  | parakeet-110m | parakeet-v3 | whisper (en) | whisper (auto) |
+--------+---------------+-------------+--------------+----------------+
| 2.5 s  |     40 ms     |    70 ms    |    892 ms    |    1744 ms     |
| 11.5 s |    124 ms     |   220 ms    |    967 ms    |    1825 ms     |
| 27 s   |    288 ms     |   526 ms    |   1255 ms    |    2151 ms     |
| 60 s   |    755 ms     |  1295 ms    |   3322 ms    |    4316 ms     |
+--------+---------------+-------------+--------------+----------------+
| v3 is  |   1.7–1.8x    |      —      |   2.4–12.7x  |   3.3–24.8x    |
|        |    slower     |             |    faster    |     faster     |
+--------+---------------+-------------+--------------+----------------+
```

**The gap is widest exactly where dictation lives.** v3 scales ~linearly with
audio length; Whisper is dominated by fixed cost per 30 s window, so at 2.5 s v3
is 25× faster and at 60 s only 3.3×. For a 2–10 s utterance v3 is sub-perceptible
(70–220 ms) where Whisper auto is 1.7–1.8 s — the difference between "instant"
and "waiting".

**Quantization is not the speed story here.** The older "don't quantize for
speed — q4_k is ~26% *slower* on CPU" entry still holds *for CPU*: it measured
per-matmul dequant against the f16 Accelerate/AMX path. On Metal that penalty is
gone, so v3-q4_k being both the small *and* the fast multilingual option is not a
contradiction with that entry — it is the Metal backend changing which axis
dominates. Labels never mention quantization; users pick a role.

**Why Whisper still owns the default.** Coverage, and it is not close: v3 does 25
European languages, Whisper ~99. **Turkish is not in v3's 25** — a Turkish clip
comes back as phonetic English mush ("Merhaba, bugün yerel…" → "Malhaba, Bugunieral
Transcription Motor Larna…"), which is the failure mode to expect for any
unsupported language: not an error, not a mislabel, just confident garbage. Since
Turkish is a primary dictation language for this project's author, v3 cannot be
the multilingual default. On languages v3 *does* cover it matched Whisper exactly
on the repo fixtures (`test/data/fr.wav`, `ru.wav`, `en.wav` — one short sentence
each, so this proves the language path works, it is not a WER ranking).

Net: role-based labels rather than engine names, because the choice a user makes
is "fast, or every language?" — not "Parakeet or Whisper?".

**Picking a language explicitly is free quality and ~0.85 s of latency
(re-verified 2026-07-26).** The models-v2 entry above found `-l tr` identical to
`-l auto` on Turkish-with-English audio; re-checked on a fresh 37.7 s Turkish
dictation with embedded English terms (`branch`, `pull rebase`, `Benchmark text`,
`design notes`), `-lang tr` was **byte-identical** to auto-detect — and to the
transcript the tray had already saved on auto. Cost of auto on the same clip:
5072 ms vs 4231 ms best-of-3 (whole-process, so load included), an ~840 ms delta
that matches the ~1.0 s detect pass measured earlier. Auto stays the default
because the language is unknown before the user speaks, but a user who always
dictates one language should set it: same text, ~0.85 s cheaper.

## Why whisper runs *with* timestamps, even though we only want text

`whisper_full` was called with `no_timestamps = true` — the obvious setting when
the caller joins segment text and throws timings away. It silently drops audio.

Under `no_timestamps`, whisper.cpp cannot recover from a decode that stops early:
on EOT it forces `seek_delta = 30 s` (`whisper.cpp:7401`), so the window always
jumps a full chunk no matter how little the model emitted, and it skips the
"decoder failed → retry at a higher temperature" branch (`:7392`). With
timestamps on, `seek_delta` comes from the last emitted timestamp token
(`:7363`), so audio the model skipped is re-decoded by the next window — the
loop self-heals. Known upstream, still open: whisper.cpp#2186.

Measured on a 131 s Turkish dictation (`whisper-turbo-q5`, auto-detect). The
final window (120 → 130.92 s) decoded into a 2-second hallucination and the loop
moved on; 11 s of speech were gone. Deterministic, 3/3 runs.

```
+---------------------------------------+-----------------------------------+
| Variant                               | Result                            |
+---------------------------------------+-----------------------------------+
| production (no_timestamps)            | tail lost, 3/3 runs               |
| only change: timestamps on            | complete and clean                |
| forced lang=tr                        | tail lost -> not a language issue |
| failing window decoded alone          | correct -> audio is fine          |
| q8_0 model, same params               | tail present but stutters         |
| Groq cloud turbo (no such flag)       | complete                          |
| English TTS, 131 s, no_timestamps     | repetition loop x15               |
+---------------------------------------+-----------------------------------+
```

Whole-file volume, same clip: 1593 chars with `no_timestamps` vs 1792 with
timestamps — 11% of the transcript was missing, tail included but not only.

Speed is a wash, and clip-dependent (wall, model load included, mean of 3):

```
+----------+-----------------+--------------+---------+
| Clip     | no_timestamps   | timestamps   | Delta   |
+----------+-----------------+--------------+---------+
| 160 s    |     3.78 s      |    4.08 s    |  +8%    |
| 131 s    |     4.24 s      |    3.57 s    | -16%    |
| 27 s     |     1.55 s      |    1.55 s    |   0     |
+----------+-----------------+--------------+---------+
```

The 131 s clip is the one that was failing: its bad decode burned
temperature-fallback retries, so fixing correctness also removed that work.
Timestamp tokens otherwise cost a few percent on clean audio.

Ruled out as causes: language, capture/VAD (per-second RMS over the lost span is
a steady 190–350, i.e. continuous speech), cross-chunk prompt (`no_context` is
already `true` by default), and quantization — q8_0 only makes the bad decode
less likely, it does not remove the trap.

No regression test ships with this: the failure is content-dependent, and a
synthetic long clip (a short sample repeated to 131 s) reproduces nothing —
whisper collapses repeated content in *both* modes. A fixture would have to be
a real multi-minute recording. The manual reproducer lives in
`whisper-tail-truncate-issue/` (probe harness + `findings.md`).

`single_segment` shares the same `seek_delta = 30 s` branch and would resurrect
this bug; do not set it.

## Why hints reach Whisper but not Parakeet

`hints.txt` used to stop at the cloud providers; the tray greyed the entry out
for anything local. That conflated two different questions — "is this on-device?"
and "can this engine be biased?" — so the gate is now `SupportsHints`, a
per-engine capability. Whisper takes the same comma-separated string the cloud
providers send as `prompt`, as `initial_prompt`. Parakeet cannot: a greedy
CTC/TDT decode has no prompt to condition on.

`carry_initial_prompt = true` goes with it. Without it whisper.cpp drops the
hint into the *rolling* context (`whisper.cpp:6958`), where decoded text pushes
it out — so a two-minute dictation is biased for its first 30 s and unbiased for
the remaining 90. With it, the hint is pinned to the front of every window's
prompt (`:6946`).

Measured A/B on two saved Turkish dictations, same model, same audio, hints file
the only difference:

```
+-------------------------------+---------------------------+
| No hints                      | With hints                |
+-------------------------------+---------------------------+
| hangi *app'leri* kullanacağız | hangi *API'leri*          |
| GitHub *Işığlarına* bakabilir | GitHub *issue'larına*     |
| Whisper *Auto Detect*         | whisper *auto-detect*     |
+-------------------------------+---------------------------+
```

**The prompt's style bleeds into the transcript** — the same behaviour OpenAI
documents for its `prompt` parameter, so this is not a local-only quirk. An
all-lowercase hints file pulled "Z'nin Whisper" down to "zinin whisper"; a list
of capitalised terms pushes mid-sentence capitals the other way. Punctuation
follows the same rule. Write `hints.txt` the way the output should look.

Ordinary prompt-conditioning side effects come with it (a repeated word at the
end of one clip, a dropped comma). Biasing is a trade, not a free win — which is
why it stays opt-in per user rather than being seeded with defaults.


## Why the login item is written but never bootstrapped (2026-07-28)

`login.Enable()` writes `~/Library/LaunchAgents/com.zee.app.plist` and stops
there. It used to follow the write with `launchctl bootstrap gui/<uid>`, which
is what a "register it now" API looks like — but bootstrap honours `RunAtLoad`
at bootstrap time, so launchd started the job immediately and the user ended up
with two tray instances: the app they clicked the toggle in, plus a
launchd-parented clone. Rejected alternatives: bootstrap then `launchctl kill`
the job (racy, and a visible flash of a second icon), or dropping `RunAtLoad`
(that key *is* the start-at-login behaviour).

launchd loads every plist in `~/Library/LaunchAgents` at login on its own, so
the file alone is the whole feature. `Enabled()` is a file-stat for the same
reason — the plist's presence, not a launchd query, is the source of truth.

`Disable()` still boots the job out before removing the plist: an entry
bootstrapped by an older build may be live in the current session.


## CTranslate2 as a local Whisper backend: measured, rejected (2026-07-31)

Evaluated in `~/Desktop/p/personal/zee-ctranslate-poc` (kept as a separate PoC
repo; nothing here imports it). Question: could CTranslate2's CPU inference
beat whisper.cpp+Metal on Apple Silicon? Measured on an M5 Pro, best of 5,
inference only, greedy decode both sides:

| Model | Clip | CT2 fp32 (Accelerate) | whisper.cpp Metal | whisper.cpp CPU |
|---|---|---|---|---|
| tiny | 1.2 s | 82 ms | 31 ms | — |
| tiny | 11 s | 115 ms | 57 ms | 101 ms |
| base.en | 1.2 s | 160 ms | 32 ms | — |
| base.en | 11 s | 240 ms | 67 ms | 193 ms |
| large-v3-turbo | 1.2 s | 2182 ms | 277 ms (q5_0) | — |
| large-v3-turbo | 11 s | 2382 ms | 308 ms (q5_0) | — |

The turbo row is the one that matters — it is the tier zee ships (measured
2026-08-03 via whisper-cli against zee's own `ggml-large-v3-turbo-q5_0.bin`;
the ~280–310 ms matches zee's in-process M5 numbers above, 272–295 ms). The
gap widens with model size (~2.5× at tiny, ~8× at turbo): CT2's fp32 CPU
weights double the memory traffic of q5_0 with no GPU offload, and CT2 int8
is actively harmful at this tier (3.8 s vs 2.4 s fp32).

Three independent findings, each enough to reject it:

- **CT2 is CPU-only on macOS** (no Metal backend; the official wheel uses
  Accelerate BLAS), and whisper.cpp *CPU-only* still edges it out.
- **CT2 int8 gives no speedup in the official macOS wheel build** (int8 ≈ fp32
  at tiny, 1.6× *slower* at turbo). CT2's headline int8 wins are x86/MKL
  territory.
- **CT2 must be fed the full 3000-frame (30 s) window.** Variable-length
  features are accepted by the encoder and are ~9× faster on short clips
  (9 ms vs 82 ms for 1.2 s, tiny), but the decoder then fails to emit EOT and
  loops to max_length when speech fills the window. Reproduced with CT2's own
  Python API (4.8.1), so it is not an integration bug; faster-whisper simply
  never exercises that path (always pads to 3000). whisper.cpp-the-CLI
  encodes variable lengths correctly — but note the symmetry: **zee cannot
  use that lever either**, because shrinking `audio_ctx` on a reused
  whisper_state garbles output (see "audio_ctx sizing"). In zee's production
  shape both engines pay full-window encoder cost on every clip; the
  variable-length advantage is real in the engine benchmark but not shippable
  here.

Integration notes if this is ever revisited: CT2 has no C API (a ~300-line
C++ shim is needed), no audio frontend (mel spectrogram implemented in the
shim via Accelerate sgemm), and no detokenizer in the C++ API (byte-level BPE
inverse in Go). The pip wheel's dylib links cleanly (Accelerate + libc++
only) but needs `install_name_tool` + ad-hoc `codesign` after extraction.


## Smaller multilingual whispers (small/medium) vs turbo-q5: measured, rejected (2026-08-03)

Question: could `ggml-small` or `ggml-medium` serve the multilingual path
faster than `whisper-turbo-q5`? Measured on M5 Pro, whisper.cpp+Metal, greedy,
best of 5, inference only, on a real 9.8 s saved Turkish dictation with
embedded English terms ("Agent Bootstrap", "ram pask" — the code-switching
case zee exists for):

| Model | `-l tr` | auto | encode | decode |
|---|---|---|---|---|
| small (fp16, 244M) | 187 ms | 245 ms | 63 ms | 111 ms |
| medium (fp16, 769M) | 381 ms | 540 ms | 168 ms | 201 ms |
| turbo q5_0 (809M, shipped) | 332 ms | 600 ms | 279 ms | 42 ms |

Two findings, each sufficient on its own:

- **medium is dominated, never an option.** It is *slower* than the quantized
  turbo on both language settings AND less accurate. The reason is structural:
  turbo pairs the large encoder with a 4-layer decoder, medium carries a full
  24-layer decoder, so medium pays 201 ms vs turbo's 42 ms on this clip. There
  is no clip length or hardware where medium wins. (An earlier draft of this
  note blamed "decode runs on CPU" — that is wrong. `sched_decode` is built
  over the same `{Metal, CPU}` backend list as the encoder,
  `third_party/whisper.cpp/src/whisper.cpp:874`. The cost is structural to
  batch-1 autoregressive decoding — one sequential pass per token, dominated
  by kernel-launch and weight-read latency rather than FLOPs — which is
  exactly the regime where GPU offload buys the least.)
- **small's speed is real but its accuracy is not good enough.** ~1.8× faster
  than turbo with a forced language, ~2.4× with auto-detect (detect cost
  scales with encoder size: +58 ms vs +265 ms). But on the code-switched clip
  it mangles exactly the English terms that are the point of the multilingual
  path: "Agent Bootstrap" → "Ejens Boots Shop", plus repetition. medium gets
  the terms mostly right ("run task" for "ram pask"); only turbo tracks the
  Groq large-v3-turbo reference closely.

Quality caveat: the accuracy read is n=1 (one clip, one speaker, model outputs
adjudicated against the Groq reference, not a human transcript) — directional,
not a WER. The latency numbers are solid. Benchmarks ran `-nt`; zee runs
timestamps-on, which adds a few percent to decode on all rows equally.

Conclusion: turbo-q5 stays. The only smaller model worth revisiting would be a
*quantized* small (q5_0) if its decode cost drops enough — but the accuracy
gap on code-switching is the disqualifier, not the speed.


## Open improvement: two-stage language detection (small detects, turbo transcribes)

Not implemented — recorded 2026-08-03 as the next thing to try on the
multilingual latency path.

Auto-detect costs a full turbo encoder pass (~265 ms on M5 Pro, ~1.0 s on M1
Pro) per clip. But language ID is a much easier problem than transcription and
does not need the big model. Measured on saved clips: `ggml-small` detects
Turkish (p=0.68 on a mixed tr/en dictation) and English (p=0.997) correctly;
`ggml-tiny` is unreliable (detected German on the same Turkish clip — do not
go below small). small's encoder pass is ~60 ms.

Plan: small-detect (~60 ms) + turbo with forced language (~332 ms) ≈ 395 ms
vs ~600 ms today — ~1.5× on the auto path with byte-identical transcription
(same turbo model, same language auto would have picked). Harden with a
two-language prior: take argmax over just the user's languages from
`whisper_lang_auto_detect`'s prob array instead of all 99. Open questions:
detection-agreement rate small-vs-turbo on real code-switched clips (fallback
to turbo-auto on low confidence?), memory cost of a second loaded model
(small-q5_0 ≈ 180 MB; detection uses only the encoder, quantization is
irrelevant), and whether the detect pass can run on a short prefix.


## Open/untested: Voxtral as a local engine (recorded 2026-08-04)

> **Partly answered 2026-08-06, by a different model.** The core hypothesis
> below — that a ~2-4B LLM decoder must be too slow locally — is now known to be
> wrong *as stated*: Qwen3-ASR-1.7B (8-bit MLX, M5 Pro) transcribes a 9.8 s clip
> in 0.58 s, i.e. a speech-LLM of that class is interactive on this hardware.
> What survives is the conclusion, for two other reasons: it is only *level*
> with whisper-auto at dictation lengths, and Voxtral has **no Turkish** in any
> variant (Mini 3B: 8 languages, Realtime 4B: 13), so it cannot serve the role
> this entry was written for. See "Qwen3-ASR-1.7B as a local engine".

**Never measured.** Voxtral exists in zee only as a cloud provider
(`transcriber/mistral.go`, `voxtral-mini-latest`). No local Voxtral has been
run, so nothing below is a measurement — it is desk research plus one
structural argument that is itself unverified. Recorded so the next person
starts from the open question rather than from scratch.

Two candidate local paths, and why neither has been tried:

- **`antirez/voxtral.c`** — runs Voxtral Realtime 4B (0.6B encoder + 3.4B
  decoder). Rejected on size alone, without benchmarking: **BF16 only, no
  quantization supported or planned**. 8.9 GB weights on disk, ~8.4 GB GPU
  weight cache, ~1.8 GB KV. zee's whole multilingual model is 547 MB. Its
  published M3 Max numbers — 284 ms encoder for 3.6 s of audio, 23.5 ms/decode
  step — put a 10 s dictation somewhere near 1.5–2 s against turbo-q5's
  ~330 ms, but that comparison was never run head-to-head here. It also
  carries its own hand-written Metal kernels rather than ggml, so it would be
  a *third* engine outside the single-ggml build, not a backend swap. Author's
  own caveat: "mostly tested against few samples, and likely requires some
  more work to be production quality."
- **Voxtral-Mini-3B via llama.cpp `mtmd`** — this is the path that is actually
  open. Quantized GGUFs exist (`ggml-org/Voxtral-Mini-3B-2507-GGUF`,
  bartowski's variants; Q4_K ≈ 2 GB), and it is the same ggml family zee
  already builds. **The reason it looks unattractive is a hypothesis, not a
  result:** a 3B LLM decoder generating token-by-token should land in the same
  regime that made whisper-medium lose to turbo (see the note above) — the
  cost of autoregressive decode scales with decoder depth × tokens and is
  latency-bound, so Metal offload does not rescue it. But that reasoning is
  extrapolated from whisper's 24-layer decoder to a different architecture on
  a different runtime, and llama.cpp is far better optimised for exactly this
  workload (batched KV, flash-attn, Metal graph reuse) than whisper.cpp's
  decode path is. **It could be wrong.** Nobody has timed a Q4 Voxtral-Mini on
  an M-series chip against turbo-q5 on the same clip.

Cheapest way to close the question, in order: (1) run the existing *cloud*
Voxtral over saved samples (`/wer-wolf`) — if its accuracy on code-switched
tr/en is not clearly better than turbo, the local port is moot regardless of
speed; (2) only if it is, time `llama-mtmd-cli` with a Q4 GGUF against
`whisper-cli` with `ggml-large-v3-turbo-q5_0.bin` on the same clip, and
settle the decode-cost hypothesis with a number.


## Parakeet 0.6b-v2 beats the 110m on English (measured 2026-08-06)

`parakeet-v2-en-large` (`tdt-0.6b-v2-f16.gguf`) was retired in models-v2 on the
premise that the 110m tied it. Re-measured against the shipped registry on an
M5 Pro (warm, best of 3, real saved dictations), the premise does not hold — and
the latency argument that backed it was a CPU-era number.

```
+----------------------+--------------+--------------+---------------+
| Model                | 1.9 s clip   | 5.2 s clip   | 182.7 s clip  |
+----------------------+--------------+--------------+---------------+
| parakeet-110m (ship) |    18 ms     |    24 ms     |   1201 ms     |
| parakeet-0.6b-v2     |    34 ms     |    41 ms     |   1815 ms     |
| parakeet-v3 (ship)   |    32 ms     |    40 ms     |   1871 ms     |
| whisper-turbo (ship) |   269 ms     |   271 ms     |   3070 ms     |
+----------------------+--------------+--------------+---------------+
```

**The 2.5x speed penalty is now ~15 ms.** On Metal the 0.6b costs 16–17 ms over
the 110m at dictation length — under perception — and runs at v3's speed, which
is expected: same architecture, same size. The old "2.5x slower" ratio came from
the CPU path (see the superseded line above); it survives only on long clips
(1.5x at 3 minutes), and there it is still 1.7x *faster* than whisper.

**Accuracy: v2 fixed both errors the 110m made, and beat whisper on a third.**
The short clips are identical across all four models — they do not discriminate.
The 183 s technical dictation does:

```
+---------------------+--------------------+------------------+---------------+
| Spoken              | 110m (ships)       | 0.6b-v2          | whisper-turbo |
+---------------------+--------------------+------------------+---------------+
| "the whisper model" | "the Visper model" | "whisper model"  | correct       |
| "quantized version" | "contised version" | "quantized"      | correct       |
| "ANE"               | "ANE"              | "ANE"            | "A&E" (3/3)   |
+---------------------+--------------------+------------------+---------------+
```

So on technical English v2 reads as whisper-class at parakeet-class latency.
Style cost: v2 keeps more verbatim repeats ("I am I'm going") that the 110m
smooths away — the same TDT-vs-corpus trait, not an error.

**Read this as a direction, not a WER.** One discriminating clip, one speaker,
differences adjudicated by ear against what was actually said. What makes it
worth acting on is that an independent, much larger eval points the same way:
the HF Open ASR leaderboard has 0.6b-v2 at 6.05% against the 110m's 7.49% (and
whisper-large-v3-turbo at ~7.8%), i.e. the ~19% relative gap the 9-sample corpus
could not resolve. That is exactly the "larger eval shows a real gap" condition
the original entry set for revisiting.

Not shipped yet — a registry change is a `models-v4` decision. Before making it:
collect a batch of fresh dictations and re-run the harness
(`internal/localbench/v2_compare_test.go`, which prints transcripts alongside
warm latency; the standard bench discards the text), and quantize v2 to q4_k
(~640 MB) — free on Metal per the entry above, and the f16 is 1.4 GB. Open
question the numbers do not answer: whether this becomes a fourth "English —
accurate" role or replaces the 110m as the English default.

## Qwen3-ASR-1.7B as a local engine: measured, not adopted (2026-08-06)

The first speech-LLM actually run on this machine rather than desk-researched
(the Voxtral entry below is still untested). Alibaba, Apache-2.0, 30 languages
**including Turkish** — the only 2025-26 release that clears zee's Turkish bar
at all, which is why it was worth the setup. 8-bit MLX
(`mlx-community/Qwen3-ASR-1.7B-8bit`, `mlx-qwen3-asr` runtime), M5 Pro, warm:

```
+---------------+---------+----------+------+---------------------------+
| Clip          | Audio   | Warm     | xRT  | whisper-turbo-q5 (M5)     |
+---------------+---------+----------+------+---------------------------+
| en-short      |   1.9 s |  0.18 s  | 10x  | 272 ms en / 535 ms auto   |
| tr-codeswitch |   9.8 s |  0.58 s  | 17x  | 295 ms en / 581 ms auto   |
| en-otel       |  70.4 s |  2.61 s  | 27x  | ~1.1 s en / ~1.3 s auto   |
| en-long       | 182.7 s |  6.50 s  | 28x  | ~2.4-4 s                  |
+---------------+---------+----------+------+---------------------------+
```

**The advertised ~36x realtime is a long-clip number and does not transfer to
dictation.** At 2-10 s the run is prefill/fixed-cost dominated, so it lands at
whisper-auto's latency, not below it — and on long clips it is *slower* than
whisper with a forced language. Same shape as every other engine here: the
figure that sells the model is measured where dictation never lives.

**Context biasing works, is free, and is the only real find.** Qwen3-ASR takes
injected vocabulary natively. On the code-switched Turkish clip, biasing with
the task's own terms recovered "Agent Bootstrap" verbatim — which neither the
unbiased run nor the Groq large-v3-turbo reference managed — at 0.57 s vs
0.58 s unbiased. That is the `hints.txt` mechanism working on a model that was
built for it, against exactly the failure mode whisper's `initial_prompt` only
weakly addresses.

Correctness was otherwise clean: Turkish auto-detected with no language hint,
no hallucination, no dropped tails (the `no_timestamps` trap has no analogue
here). It transcribes verbatim, keeping "I'm I'm", "Um", "Uh" that whisper and
parakeet smooth away — arguably wrong for dictation-to-clipboard.

Not adopted: no accuracy win over turbo on the hard Turkish clip (that clip is
garbled in every engine including the cloud reference — the audio is the
problem), ~2.3 GB resident against whisper's ~600 MB, and it is a **third engine
outside the one-ggml build** — MLX or ONNX, not a backend swap. Revisit if the
biasing win generalises over a batch of code-switched samples, since that is a
capability whisper cannot match rather than a margin it can be tuned into.
Raw transcripts and timings: scratchpad `qwen-asr-results.md` / `qwen-raw.json`.

## STT landscape: what comparable apps ship (reference, verified 2026-08-03)

Reference material for engine decisions, not a decision itself. Verified by
reading the dependency manifests of the open-source apps (VoiceInk, Handy,
Voquill cloned and inspected); superwhisper is closed-source, so its row is
their marketing plus what's publicly known.

| App | Local engines | Models | Acceleration | Turkish path |
|---|---|---|---|---|
| zee | whisper.cpp + parakeet.cpp (shared ggml) | whisper-turbo-q5_0, Parakeet 110m-en / v3-q4_k | Metal | whisper-turbo (only option) |
| VoiceInk (OSS, Swift) | whisper.cpp + FluidAudio (CoreML Parakeet/Nemotron) + Apple Speech + cloud | whisper ggml (non-quantized paired w/ **Core ML encoder bundles**; q5/q8 without), Parakeet TDT v2/v3, Nemotron streaming | Metal (whisper), **ANE** (Parakeet) | whisper only — FluidAudio v3 has no tr |
| Handy (OSS, Rust/Tauri) | transcribe-cpp (whisper GGUF) + transcribe-rs (ONNX: Parakeet, Moonshine, SenseVoice, GigaAM) | whisper ggml, parakeet v2/v3 int8-ONNX | Metal, Vulkan (Win), CPU ONNX | whisper only |
| Voquill (OSS, Rust/Tauri) | whisper-rs (whisper.cpp) + cloud | whisper ggml | "optional GPU" | whisper only |
| superwhisper (closed) | unknown, whisper family | "Whisper … Large" tiers + cloud LLMs | claims Apple-Silicon-optimized | whisper only |

Takeaways:

- **Nobody beats zee's Turkish path.** Every app runs the same whisper.cpp for
  it; the only extra trick in the field is VoiceInk pairing *non-quantized*
  whisper models with Core ML encoder bundles — the option already measured
  and declined here (−15–21% on M5 for +1.2 GB/model, and upstream whisper.cpp
  dropped Core ML after v1.9.1). Their payload/memory for equivalent latency
  is worse than turbo-q5_0.
- **The speed everyone else markets is Parakeet**, which zee already ships
  (110m: 1.9 s → 18 ms on M5). VoiceInk's one real difference is running it on
  the ANE via CoreML/FluidAudio instead of Metal-ggml — lower power, but
  single-digit ms at zee's latencies; not worth a second engine.
- **Choices worth borrowing, none urgent:** streaming partial results
  (VoiceInk via Parakeet-EOU/Nemotron — the only categorically faster *feel*,
  English-only today); Apple Speech as a zero-download built-in fallback
  (offline, supports tr-TR, accuracy below turbo); ONNX Parakeet (Handy) only
  matters for a Windows/Linux port.

**Model-field refresh, 2026-08-06.** Desk research, not measurement — recorded
so the next survey starts here rather than at the leaderboard.

- **whisper-large-v3-turbo is no longer on the accuracy frontier at any latency
  point.** The open-weights 5.0-5.6% WER tier is now ARK-ASR-0.6B/3B,
  granite-speech-4.1-2b and canary-qwen-2.5b, against turbo's ~7.8%. None has a
  proven Apple-Silicon latency story, and all are LLM-decoder models, i.e. the
  regime that already lost to turbo when whisper-medium was measured.
- **A 2026 code-switching benchmark rates turbo *worst* of the models tested**
  (es/fr/de-English; without a forced language it drifts into translating).
  No Turkish-English corpus exists, so zee's own samples remain the only
  evidence for the case that actually matters here.
- **The "nobody beats zee's Turkish path" takeaway holds.** Every 2026 arrival
  that is faster than turbo still has no Turkish: Canary 1b-v2 (25 European
  languages, same gap as Parakeet v3), Voxtral Mini 3B (8) and Realtime 4B (13),
  Kyutai STT (en/fr), SenseVoice-small (5 Asian + en), Phi-4-multimodal audio
  (8). Qwen3-ASR is the sole exception — measured above, not adopted.
- **Apple SpeechTranscriber (macOS 26) does have Turkish**, and is free and
  zero-download, but commits to **one language per session** — so it cannot do
  the mid-utterance tr/en switching zee exists for. Still the cheapest possible
  fallback if that ever stops mattering.
- **whisper.cpp is still v1.9.1**; nothing since changes multilingual quality,
  and there is no large-v4 or new open OpenAI ASR release.
- **`transcribe.cpp`** (handy-computer, MIT, Mozilla.ai-backed) is the piece of
  infrastructure worth tracking: one ggml/GGUF/Metal runtime covering ~16
  families (Parakeet, Canary, Canary-Qwen, Qwen3-ASR, Voxtral, Moonshine,
  SenseVoice, Granite). It would make that whole 5% tier testable without a
  per-model integration, and could eventually replace maintaining separate
  parakeet.cpp and whisper.cpp paths.

## The felt-latency tail is paste, not inference (measured 2026-08-06)

`felt_latency` used to log one number, so "parakeet feels slow" could not be
attributed. It now itemizes the release→text window (tail wait, mic stop, PCM
convert, inference, clipboard save, paste copy, paste keystroke, plus an
`unaccounted_ms` remainder). What that showed, over ~57 dictations:

- **Felt latency was near-constant (~900–1100 ms) while inference swung
  100→700 ms.** That signature means fixed overhead dominated, not the model.
  It also explains an earlier misreading: inference looked *inversely*
  correlated with clip length, which was an artifact of the constant total.
- **The overhead was the clipboard, not the engine.** On a representative
  22.6 s clip: inference 298 ms against 255 ms of serial paste work —
  `paste_copy_ms` 141 ms (pbcopy) + `paste_key_ms` 114 ms (keybd_event).

Two distinct causes, both now removed on macOS (`clipboard/clipboard_darwin.m`):

- **fork() cost scales with resident memory.** atotto's Copy/Read exec pbcopy
  and pbpaste; fork freezes every thread while the kernel clones page tables,
  which is ~140–250 ms once a local model is resident (RSS ~660 MB). Replaced
  with NSPasteboard: **172 µs** for a copy, ~800x faster, and it removes the
  `LC_CTYPE=en_US.UTF-8` hack that existed only so the pbcopy *child* would not
  mangle Turkish characters — NSString carries the encoding itself.
- **keybd_event slept 100 ms between key down and key up** (`tapKey`, with the
  comment "ignore if speed is most in my test system"). Replaced with a direct
  CGEvent pair, deliberately using the same mechanism it had proven — NULL
  source, `kCGAnnotatedSessionEventTap`, explicit flags — minus the sleep.

**The feedback beep was blocking the release path (M5 Pro).** With the clipboard
fixed, a stubborn ~50 ms remainder was left in `unaccounted_ms`. One-off probes
across every unmeasured segment found all of them clean (tray 0.18 ms, meter
stats 0.09 ms, goroutine handoff 0.04 ms, `updatesDone` 0.00 ms, `captureRSS`
23 us despite gopsutil dlopen/dlclose-ing libproc per call) except one:
`audio.PlayEnd()` at **46-49 ms**, matching the remainder almost exactly.

AVAudioPlayer's `-play` blocks while it starts the audio hardware. `prepareToPlay`
at load does not prevent it, and the cost recurs every time — the hardware powers
back down between beeps — so it was not warmup. The header comment claiming
"fire-and-forget" was aspirational; the call was inline and synchronous. Now
dispatched to a serial queue (`audio/beep_darwin.m`): the tone still sounds at the
same instant, the caller no longer waits. It was on the push-to-talk path *twice*
per dictation, so `PlayStart` at press was paying it too, inflating
`press_to_record_ms`.

**The clipboard *save* fork was already free, and that is worth remembering.**
`clip_save_ms` ran 241 ms but `clip_wait_ms` was 0: it overlaps inference by
design (saved lazily after recording ends, never during the press — see
`main.go`). It only becomes visible when inference is faster than the fork, so
the fix had to cover Read as well as Copy, not just the obviously-serial half.

Linux keeps the atotto backend: no local model inflates RSS there, so the fork
is cheap and a second native backend would not pay for itself.
