//go:build darwin && arm64

// Package parakeet is a thin cgo wrapper over mudler parakeet.cpp's flat C-API
// (third_party/parakeet.cpp/include/parakeet_capi.h). It loads a GGUF model once
// and transcribes mono 16 kHz float32 PCM — no network. Built with the Metal
// backend (parakeet v0.4.0+): the GPU runs what it can and falls back to CPU for
// unsupported ops. The embedded metallib keeps the single-binary story intact.
//
// Build: the static archives under third_party/parakeet.cpp/build-release/ must
// exist before `go build` links this package. `make build` runs the cmake step
// first. Gated to darwin/arm64; every other platform compiles the stub.
package parakeet

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/parakeet.cpp/include
#cgo LDFLAGS: ${SRCDIR}/../../third_party/parakeet.cpp/build-release/libparakeet.a
#cgo LDFLAGS: ${SRCDIR}/../../third_party/parakeet.cpp/build-release/third_party/ggml/src/libggml.a
#cgo LDFLAGS: ${SRCDIR}/../../third_party/parakeet.cpp/build-release/third_party/ggml/src/libggml-cpu.a
#cgo LDFLAGS: ${SRCDIR}/../../third_party/parakeet.cpp/build-release/third_party/ggml/src/ggml-blas/libggml-blas.a
#cgo LDFLAGS: ${SRCDIR}/../../third_party/parakeet.cpp/build-release/third_party/ggml/src/ggml-metal/libggml-metal.a
#cgo LDFLAGS: ${SRCDIR}/../../third_party/parakeet.cpp/build-release/third_party/ggml/src/libggml-base.a
#cgo LDFLAGS: -lc++ -lm -framework Accelerate -framework Metal -framework MetalKit -framework Foundation
#include <stdlib.h>
#include "parakeet_capi.h"

// ggml logs backend/pipeline chatter to stderr by default (~90 lines per
// Metal run). Route it to a no-op. Declared here (not via ggml.h, which isn't
// on the include path) — the symbol is in the linked ggml archive.
typedef void (*ggml_log_callback)(int level, const char *text, void *user_data);
extern void ggml_log_set(ggml_log_callback cb, void *user_data);
static void zee_ggml_silent(int l, const char *t, void *u) { (void)l; (void)t; (void)u; }
static void zee_ggml_hush(void) { ggml_log_set(zee_ggml_silent, 0); }
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

// Decoder selects the model head passed to the C-API.
const (
	DecoderDefault = 0 // by arch: transducer for tdt/rnnt, CTC for ctc
	DecoderCTC     = 1 // force the CTC head
	DecoderTDT     = 2 // force the transducer (TDT/RNN-T) head
)

// Available reports whether local Parakeet transcription is compiled in.
func Available() bool { return true }

// Ctx wraps one loaded GGUF model. Transcribe is serialised by an internal
// mutex (push-to-talk is serial; the C ctx is not concurrency-safe), and Close
// waits for any in-flight Transcribe before freeing the model.
type Ctx struct {
	mu  sync.Mutex
	ptr *C.parakeet_ctx
}

var hushOnce sync.Once

// New loads a GGUF model from path. The returned Ctx must be Closed. On the
// Metal build it also warms up: one throwaway transcribe so the GPU pipelines
// compile now (at startup) rather than stalling the first real dictation.
func New(path string) (*Ctx, error) {
	hushOnce.Do(func() { C.zee_ggml_hush() })

	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	ptr := C.parakeet_capi_load(cPath)
	if ptr == nil {
		// load failures have no ctx, so pass NULL to last_error.
		return nil, fmt.Errorf("parakeet: load %q: %s", path, C.GoString(C.parakeet_capi_last_error(nil)))
	}
	c := &Ctx{ptr: ptr}
	c.Transcribe(make([]float32, 16000), DecoderDefault) // 1s of silence; ignore result
	return c, nil
}

// Transcribe runs the model over mono 16 kHz float32 PCM and returns the
// transcript. decoder is one of the Decoder* constants.
func (c *Ctx) Transcribe(pcm []float32, decoder int) (string, error) {
	if len(pcm) == 0 {
		return "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ptr == nil {
		return "", fmt.Errorf("parakeet: transcribe on closed model")
	}

	out := C.parakeet_capi_transcribe_pcm(c.ptr,
		(*C.float)(unsafe.Pointer(&pcm[0])), C.int(len(pcm)), 16000, C.int(decoder))
	if out == nil {
		return "", fmt.Errorf("parakeet: transcribe: %s", C.GoString(C.parakeet_capi_last_error(c.ptr)))
	}
	defer C.parakeet_capi_free_string(out)
	return C.GoString(out), nil
}

// Close frees the model. Safe to call more than once.
func (c *Ctx) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ptr != nil {
		C.parakeet_capi_free(c.ptr)
		c.ptr = nil
	}
}
