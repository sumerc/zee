//go:build darwin && arm64

// Package whisper is a thin cgo wrapper over whisper.cpp's C API. It loads a
// ggml model once and transcribes mono 16 kHz float32 PCM — no network. Unlike
// parakeet it is multilingual and takes a language per call ("" = auto-detect).
//
// It links the SAME ggml archives parakeet builds (one ggml in the process, and
// it is parakeet's patched one). `make whisper-lib` installs parakeet's ggml to
// a prefix and builds libwhisper.a against it; without that, whisper would
// compile against its own vendored ggml headers while linking parakeet's
// archives — silent struct-layout corruption rather than a link error.
//
// Gated to darwin/arm64; every other platform compiles the stub.
package whisper

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/whisper.cpp/include
#cgo CFLAGS: -I${SRCDIR}/../../third_party/parakeet.cpp/build-release/ggml-prefix/include
#cgo LDFLAGS: ${SRCDIR}/../../third_party/whisper.cpp/build-release/src/libwhisper.a
#cgo LDFLAGS: ${SRCDIR}/../../third_party/parakeet.cpp/build-release/third_party/ggml/src/libggml.a
#cgo LDFLAGS: ${SRCDIR}/../../third_party/parakeet.cpp/build-release/third_party/ggml/src/libggml-cpu.a
#cgo LDFLAGS: ${SRCDIR}/../../third_party/parakeet.cpp/build-release/third_party/ggml/src/ggml-blas/libggml-blas.a
#cgo LDFLAGS: ${SRCDIR}/../../third_party/parakeet.cpp/build-release/third_party/ggml/src/ggml-metal/libggml-metal.a
#cgo LDFLAGS: ${SRCDIR}/../../third_party/parakeet.cpp/build-release/third_party/ggml/src/libggml-base.a
#cgo LDFLAGS: -lc++ -lm -framework Accelerate -framework Metal -framework MetalKit -framework Foundation
#include <stdlib.h>
#include <string.h>
#include "whisper.h"

// ggml and whisper both log backend/pipeline chatter to stderr by default.
// Route both to a no-op. whisper.h already pulls in ggml.h, so ggml_log_set and
// the ggml_log_callback typedef come from there (parakeet's wrapper has to
// declare them by hand only because its header does not include ggml.h).
static void zee_wsp_silent(enum ggml_log_level l, const char *t, void *u) {
    (void)l; (void)t; (void)u;
}
static void zee_wsp_hush(void) {
    ggml_log_set(zee_wsp_silent, 0);
    whisper_log_set(zee_wsp_silent, 0);
}

// zee_wsp_transcribe runs one whisper_full pass and returns the concatenated
// segment text as a malloc'd C string (caller frees), or NULL on failure. Doing
// the param setup and segment join here keeps the Go side to a two-argument
// call and avoids marshalling whisper_full_params' nested structs through cgo.
static char *zee_wsp_transcribe(struct whisper_context *ctx, const float *pcm,
                                int n, const char *lang, const char *prompt,
                                int audio_ctx) {
    struct whisper_full_params p = whisper_full_default_params(WHISPER_SAMPLING_GREEDY);
    p.print_progress   = false;
    p.print_realtime   = false;
    p.print_timestamps = false;
    p.print_special    = false;
    // no_timestamps stays FALSE even though we only want text: it selects a
    // decoder path that silently drops audio. See audioCtxFor's sibling note
    // below and docs/design-notes.md — with it set, whisper advances the window
    // a fixed 30 s per decode and skips the retry-on-failed-decode path, so
    // whatever the model does not emit in a window is lost for good.
    p.translate        = false;   // transcribe in-language, never translate to English
    p.language         = lang;    // "auto" => detect (one extra decode step; see audioCtxFor)
    p.audio_ctx        = audio_ctx;  // 0 = full window; see audioCtxFor

    // Vocabulary hints ride in as the initial prompt — the same string the
    // cloud providers send as `prompt`. carry_initial_prompt keeps it pinned to
    // the front of EVERY window's prompt (whisper.cpp:6946); without it the
    // hint lands in the rolling context and is diluted away after the first
    // 30 s, which for a two-minute dictation means most of the audio decodes
    // unbiased.
    if (prompt != NULL && prompt[0] != '\0') {
        p.initial_prompt       = prompt;
        p.carry_initial_prompt = true;
    }

    if (whisper_full(ctx, p, pcm, n) != 0) {
        return NULL;
    }

    const int ns = whisper_full_n_segments(ctx);
    size_t total = 1;
    for (int i = 0; i < ns; i++) {
        total += strlen(whisper_full_get_segment_text(ctx, i));
    }
    char *out = (char *)malloc(total);
    if (out == NULL) {
        return NULL;
    }
    // Append at a running offset rather than strcat, which rescans the whole
    // destination on every segment — quadratic in transcript length, and a long
    // dictation is thousands of segments.
    char *w = out;
    for (int i = 0; i < ns; i++) {
        const char *seg = whisper_full_get_segment_text(ctx, i);
        size_t n = strlen(seg);
        memcpy(w, seg, n);
        w += n;
    }
    *w = '\0';
    return out;
}
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

// sampleRate is the only rate whisper accepts.
const sampleRate = 16000

// audioCtxFor returns whisper's audio_ctx: 0 means "use the full 1500-frame
// window", which is the only setting that transcribes correctly here.
//
// Sizing the window to the clip — min(1500, ceil(sec*50*1.3)) — is a large
// short-clip win on paper (~85% off encode; upstream issue #1855 proposes the
// same idea, never maintainer-validated). It is unusable here, and the
// mechanism is now pinned by TestAudioCtxFaultMatrix:
//
//	SHRINKING audio_ctx on a reused whisper_state garbles; growing is fine.
//
//	400 → 400 → 400, and clipA→clipB at one size    correct
//	200 → 400 (grow)                                correct
//	400 → 200 (shrink)                              garbage
//	0/1500 (full) → 400 (shrink)                    garbage, and stays garbled
//
// Reproduced identically on pure upstream ggml v0.13.0 (acrepro.c in the POC
// repo) — an upstream whisper.cpp/ggml bug, not one of parakeet's ggml patches.
// Content and warm-up are red herrings: silence at a constant size is harmless;
// our 1 s warm-up only triggered it because it ran at the full window, turning
// every later sized call into a shrink.
//
// At ac=0 every pass uses the full grid, sizes never change, and the bug cannot
// trigger. Re-run the fault matrix (ZEE_AC_DEBUG=1) before believing anything.
//
// Re-verified 2026-07-26 after no_timestamps was turned off: the matrix is
// UNCHANGED (D/F/G/H/I/J/L garble, A/B/C/E/K pass). Different layer — that fix
// is in the decoder loop, this fault is in the encoder state. Upstream has not
// fixed it either: 154 commits since v1.9.1 (still the newest tag), none
// touching exp_n_audio_ctx.
//
// This comment used to say auto-detect shrinks WITHIN one whisper_full call and
// that this killed the lever outright. Measured 2026-07-31: wrong. The detect
// encode reads whatever the PREVIOUS call left in exp_n_audio_ctx, so it only
// shrinks from a cold state (0, which every read site expands to 1500). Primed
// once at a small size, later auto calls at a LARGER size are grows and are
// correct (matrix cases M/N).
//
// Superseded 2026-08-06 for the cold case: patches/whisper.cpp now assigns
// exp_n_audio_ctx before the detect encode, so one call encodes at one size and
// case H (cold, auto, sized) passes. Sizing on a REUSED state still garbles
// (D/F/G/I/J/L unchanged), so the lever needs a fresh whisper_state per
// utterance — measured ~10 ms, i.e. affordable. Returning 0 stays a deliberate
// choice: sizing is worth a further ~1.7x but does not preserve the transcript
// word-for-word. See design-notes "audio_ctx sizing".
func audioCtxFor(int) int { return 0 }

// Available reports whether local Whisper transcription is compiled in.
func Available() bool { return true }

// Ctx wraps one loaded whisper model. Transcribe is serialised by an internal
// mutex (push-to-talk is serial; the C ctx is not concurrency-safe), and Close
// waits for any in-flight Transcribe before freeing the model.
type Ctx struct {
	mu  sync.Mutex
	ptr *C.struct_whisper_context
}

var hushOnce sync.Once

// New loads a ggml whisper model from path. The returned Ctx must be Closed. It
// also warms up: one throwaway transcribe so the backend's first-use init
// (Metal pipeline compilation, buffer/kernel setup) happens now rather than
// stalling the first real dictation. The warm-up runs in auto-detect mode
// because that is the default path.
func New(path string) (*Ctx, error) {
	c, err := newNoWarm(path)
	if err != nil {
		return nil, err
	}
	c.Transcribe(make([]float32, sampleRate), "", "") // 1s of silence; ignore result
	return c, nil
}

// newNoWarm loads the model without the warm-up pass. Split out so tests can
// probe a genuinely cold context (the audio_ctx fault matrix needs it).
func newNoWarm(path string) (*Ctx, error) {
	hushOnce.Do(func() { C.zee_wsp_hush() })

	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	params := C.whisper_context_default_params()
	ptr := C.whisper_init_from_file_with_params(cPath, params)
	if ptr == nil {
		return nil, fmt.Errorf("whisper: load %q failed", path)
	}
	return &Ctx{ptr: ptr}, nil
}

// Transcribe runs the model over mono 16 kHz float32 PCM and returns the
// transcript. lang is an ISO-639-1 code; "" means auto-detect, which is the
// only mode that survives code-switching — a wrong forced language garbles the
// output rather than merely mislabelling it. Detection is close to free: it
// shares its encoder pass with the first decode window (patches/whisper.cpp).
//
// hints is optional vocabulary biasing (the same comma-separated string the
// cloud providers take as `prompt`); "" disables it.
func (c *Ctx) Transcribe(pcm []float32, lang, hints string) (string, error) {
	return c.transcribeAt(pcm, lang, hints, audioCtxFor(len(pcm)))
}

// transcribeAt is Transcribe with an explicit audio_ctx (0 = full window).
// Production always goes through Transcribe/audioCtxFor; tests use this to
// exercise reduced windows directly.
func (c *Ctx) transcribeAt(pcm []float32, lang, hints string, audioCtx int) (string, error) {
	if len(pcm) == 0 {
		return "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ptr == nil {
		return "", fmt.Errorf("whisper: transcribe on closed model")
	}

	if lang == "" {
		lang = "auto"
	}
	cLang := C.CString(lang)
	defer C.free(unsafe.Pointer(cLang))
	cHints := C.CString(hints)
	defer C.free(unsafe.Pointer(cHints))

	out := C.zee_wsp_transcribe(c.ptr,
		(*C.float)(unsafe.Pointer(&pcm[0])), C.int(len(pcm)),
		cLang, cHints, C.int(audioCtx))
	if out == nil {
		return "", fmt.Errorf("whisper: transcribe failed")
	}
	defer C.free(unsafe.Pointer(out))
	return C.GoString(out), nil
}

// Close frees the model. Safe to call more than once.
func (c *Ctx) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ptr != nil {
		C.whisper_free(c.ptr)
		c.ptr = nil
	}
}
