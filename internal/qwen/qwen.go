//go:build darwin && arm64

// Package qwen is a thin cgo wrapper over antirez/qwen-asr, a dependency-free
// C implementation of Qwen3-ASR. It loads a model directory once and
// transcribes mono 16 kHz float32 PCM — no network.
//
// POC (qwen-asr-int branch). Three things separate it from the other two local
// engines and decide where it can be used:
//
//   - CPU only. The engine has no GPU backend at all; matmuls go through
//     Accelerate BLAS. Parakeet and Whisper both run on Metal here, so expect a
//     different performance class, not a different constant factor.
//   - Weights are bf16, unquantized (0.6B = 1.8 GB on disk, and it is read into
//     RAM at load). There is no quantized format upstream yet.
//   - The model is a DIRECTORY (config.json, vocab.json, merges.txt,
//     model.safetensors), not a single file like gguf. localmodel.Model.IsDir
//     carries that distinction.
//
// Unlike parakeet/whisper it shares no ggml with anything, so there is no
// cross-library ggml hazard here — it links its own kernels plus Accelerate.
//
// Gated to darwin/arm64; every other platform compiles the stub.
package qwen

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/qwen-asr
#cgo LDFLAGS: ${SRCDIR}/../../third_party/qwen-asr/build-release/libqwen_asr.a
#cgo LDFLAGS: -lm -lpthread -framework Accelerate
#include <stdlib.h>
#include "qwen_asr.h"
#include "qwen_asr_kernels.h"

// qwen_verbose is an extern int, which cgo cannot assign to directly.
static void zee_qwen_set_verbose(int v) { qwen_verbose = v; }
*/
import "C"

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"unsafe"
)

// sampleRate is the only rate the engine accepts.
const sampleRate = 16000

// Available reports whether local Qwen transcription is compiled in.
func Available() bool { return true }

// threadsOnce sizes the engine's worker pool exactly once per process. The pool
// is global C state (one static in qwen_asr_kernels.c), not per-context, and
// resizing it tears down and respawns every worker — so it must not be redone
// on each model load.
var threadsOnce sync.Once

// setThreads opens up the worker pool. This is NOT optional tuning: the library
// defaults to n_threads=1 and only upstream's CLI raises it, so a caller that
// forgets runs the whole model on one core. ZEE_QWEN_THREADS overrides for
// experiments; anything <= 0 or unset means "every CPU", matching the CLI.
func setThreads() {
	n := int(C.qwen_get_num_cpus())
	if v, err := strconv.Atoi(os.Getenv("ZEE_QWEN_THREADS")); err == nil && v > 0 {
		n = v
	}
	C.qwen_set_threads(C.int(n))

	// ZEE_QWEN_VERBOSE=1 makes the engine print its own mel/encoder/prefill/
	// decode timings to stderr — the only way to see where a slow transcription
	// actually went, since zee's own log records one inference_ms total.
	if v, err := strconv.Atoi(os.Getenv("ZEE_QWEN_VERBOSE")); err == nil && v > 0 {
		C.zee_qwen_set_verbose(C.int(v))
	}
}

// Languages lists the language NAMES the model accepts ("English", "Turkish").
// The engine matches on the name, not an ISO-639-1 code, so callers holding
// codes must map first — transcriber/qwen.go does that via its label table.
func Languages() []string {
	return strings.Split(C.GoString(C.qwen_supported_languages_csv()), ",")
}

// Ctx wraps one loaded Qwen model. Transcribe is serialised by an internal
// mutex (push-to-talk is serial, and the C context carries a KV cache plus
// persistent scratch buffers, so it is not concurrency-safe). Close waits for
// any in-flight Transcribe before freeing.
type Ctx struct {
	mu  sync.Mutex
	ptr *C.qwen_ctx_t
}

// New loads a Qwen model from a directory. The returned Ctx must be Closed.
//
// Deliberately NOT warmed up the way whisper.New is: this engine has no Metal
// pipeline to compile, and a warm-up pass costs a full CPU decode (seconds, not
// milliseconds) on every startup. Load already touches all the weights.
func New(dir string) (*Ctx, error) {
	threadsOnce.Do(setThreads)

	cDir := C.CString(dir)
	defer C.free(unsafe.Pointer(cDir))

	ptr := C.qwen_load(cDir)
	if ptr == nil {
		return nil, fmt.Errorf("qwen: load %q failed", dir)
	}
	return &Ctx{ptr: ptr}, nil
}

// Transcribe runs the model over mono 16 kHz float32 PCM and returns the
// transcript.
//
// lang is a language NAME ("Turkish"); "" means auto-detect, which is the
// engine's default and costs nothing extra here — unlike whisper, where
// auto-detect buys a second encoder pass. An unsupported name is an error
// rather than a silent fallback, so a bad mapping surfaces in testing instead
// of quietly transcribing in the wrong language.
//
// hints is optional vocabulary biasing, fed in as the system prompt.
func (c *Ctx) Transcribe(pcm []float32, lang, hints string) (string, error) {
	if len(pcm) == 0 {
		return "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ptr == nil {
		return "", fmt.Errorf("qwen: transcribe on closed model")
	}

	cLang := C.CString(lang)
	defer C.free(unsafe.Pointer(cLang))
	if C.qwen_set_force_language(c.ptr, cLang) != 0 {
		return "", fmt.Errorf("qwen: unsupported language %q", lang)
	}

	cHints := C.CString(hints)
	defer C.free(unsafe.Pointer(cHints))
	C.qwen_set_prompt(c.ptr, cHints)

	out := C.qwen_transcribe_audio(c.ptr,
		(*C.float)(unsafe.Pointer(&pcm[0])), C.int(len(pcm)))
	if out == nil {
		return "", fmt.Errorf("qwen: transcribe failed")
	}
	defer C.free(unsafe.Pointer(out))
	return strings.TrimSpace(C.GoString(out)), nil
}

// Stats reports timings from the most recent Transcribe on this context, in
// milliseconds. Encode is mel + audio encoder, Decode is prefill + generation.
// Used by the benchmark to attribute cost; zero before the first call.
func (c *Ctx) Stats() (total, encode, decode float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ptr == nil {
		return 0, 0, 0
	}
	return float64(c.ptr.perf_total_ms),
		float64(c.ptr.perf_encode_ms),
		float64(c.ptr.perf_decode_ms)
}

// Close frees the model. Safe to call more than once.
func (c *Ctx) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ptr != nil {
		C.qwen_free(c.ptr)
		c.ptr = nil
	}
}
