//go:build darwin && arm64

// Package s1 is a thin cgo wrapper over llama.cpp's C API running Superwhisper's
// S1-mini, a Qwen3-0.6B fine-tune that normalizes raw ASR transcripts into clean
// written text (fillers dropped, punctuation and casing applied, spoken numbers
// and dates written out). English-only; steering is a fixed control line, not
// chat. It loads a GGUF once and rewrites text — no network.
//
// It links the SAME ggml archives parakeet builds (one ggml in the process, and
// it is parakeet's patched one — see whisper.go for why two copies corrupt
// silently). `make s1-lib` builds libllama.a against that installed prefix with
// -DLLAMA_USE_SYSTEM_GGML=ON.
//
// Gated to darwin/arm64; every other platform compiles the stub.
package s1

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/llama.cpp/include
#cgo CFLAGS: -I${SRCDIR}/../../third_party/parakeet.cpp/build-release/ggml-prefix/include
#cgo LDFLAGS: ${SRCDIR}/../../third_party/llama.cpp/build-release/src/libllama.a
#cgo LDFLAGS: ${SRCDIR}/../../third_party/parakeet.cpp/build-release/third_party/ggml/src/libggml.a
#cgo LDFLAGS: ${SRCDIR}/../../third_party/parakeet.cpp/build-release/third_party/ggml/src/libggml-cpu.a
#cgo LDFLAGS: ${SRCDIR}/../../third_party/parakeet.cpp/build-release/third_party/ggml/src/ggml-blas/libggml-blas.a
#cgo LDFLAGS: ${SRCDIR}/../../third_party/parakeet.cpp/build-release/third_party/ggml/src/ggml-metal/libggml-metal.a
#cgo LDFLAGS: ${SRCDIR}/../../third_party/parakeet.cpp/build-release/third_party/ggml/src/libggml-base.a
#cgo LDFLAGS: -lc++ -lm -framework Accelerate -framework Metal -framework MetalKit -framework Foundation
#include <stdlib.h>
#include "llama.h"

static void zee_s1_silent(enum ggml_log_level l, const char *t, void *u) {
    (void)l; (void)t; (void)u;
}
static void zee_s1_hush(void) {
    llama_log_set(zee_s1_silent, 0);
    ggml_log_set(zee_s1_silent, 0);
}
*/
import "C"

import (
	"fmt"
	"strings"
	"sync"
	"unsafe"
)

// Available reports whether local S1 normalization is compiled in.
func Available() bool { return true }

// The model card is strict: this exact system prompt, then a control line, then
// the transcript. Greedy decoding, thinking disabled (the empty <think> block in
// the template below is what apply_chat_template(enable_thinking=False) emits
// for Qwen3 — without it the model produces blank output).
const systemPrompt = "You are a text normalizer for speech-to-text transcripts. " +
	"The input begins with a control line specifying the styling, structure, and context settings; " +
	"clean the transcript to match those settings and output only the cleaned text."

const controlLine = "[Styling: semi-formal] [Structure: prose] [Context: general]"

// nCtx bounds one pass: the card says to keep single passes under ~1000 input
// tokens, and output is ~1.3× input, so 2048 covers the whole dictation range.
// Longer transcripts are returned unchanged rather than chunked — push-to-talk
// clips that overflow this are rare enough that chunking isn't worth its seams.
const nCtx = 2048

// Ctx wraps one loaded GGUF model plus its inference context. Normalize is
// serialised by an internal mutex (push-to-talk is serial; the llama context is
// not concurrency-safe), and Close waits for any in-flight call.
type Ctx struct {
	mu    sync.Mutex
	model *C.struct_llama_model
	lctx  *C.struct_llama_context
	smpl  *C.struct_llama_sampler
	vocab *C.struct_llama_vocab
}

var initOnce sync.Once

// New loads the S1-mini GGUF from path. The returned Ctx must be Closed. It
// warms up with one tiny pass so the backend's first-use init (GPU pipeline
// compilation) happens now rather than stalling the first real dictation.
func New(path string) (*Ctx, error) {
	initOnce.Do(func() {
		C.zee_s1_hush()
		C.llama_backend_init()
	})

	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	mp := C.llama_model_default_params()
	mp.n_gpu_layers = 99
	model := C.llama_model_load_from_file(cPath, mp)
	if model == nil {
		return nil, fmt.Errorf("s1: load %q failed", path)
	}

	cp := C.llama_context_default_params()
	cp.n_ctx = nCtx
	cp.n_batch = nCtx
	lctx := C.llama_init_from_model(model, cp)
	if lctx == nil {
		C.llama_model_free(model)
		return nil, fmt.Errorf("s1: context init failed")
	}

	smpl := C.llama_sampler_chain_init(C.llama_sampler_chain_default_params())
	C.llama_sampler_chain_add(smpl, C.llama_sampler_init_greedy())

	c := &Ctx{
		model: model,
		lctx:  lctx,
		smpl:  smpl,
		vocab: C.llama_model_get_vocab(model),
	}
	c.Normalize("warm up pass") // ignore result; primes the Metal pipelines
	return c, nil
}

// Normalize rewrites one raw transcript as clean written text. On any overflow
// or decode failure it returns the input unchanged with the error — the caller
// always has usable text.
func (c *Ctx) Normalize(text string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lctx == nil {
		return text, fmt.Errorf("s1: closed")
	}

	prompt := "<|im_start|>system\n" + systemPrompt + "<|im_end|>\n" +
		"<|im_start|>user\n" + controlLine + "\n" + text + "<|im_end|>\n" +
		"<|im_start|>assistant\n<think>\n\n</think>\n\n"

	cPrompt := C.CString(prompt)
	defer C.free(unsafe.Pointer(cPrompt))

	// The special tokens are already in the string (parse_special), and Qwen3
	// adds no BOS (add_special false).
	maxTok := len(prompt) + 16
	toks := make([]C.llama_token, maxTok)
	n := C.llama_tokenize(c.vocab, cPrompt, C.int32_t(len(prompt)),
		&toks[0], C.int32_t(maxTok), false, true)
	if n <= 0 {
		return text, fmt.Errorf("s1: tokenize failed (%d)", n)
	}
	nPrompt := int(n)

	// Output budget: ~1.3× input + slack, and never past the context window.
	maxNew := nPrompt*13/10 + 48
	if nPrompt+maxNew > nCtx {
		maxNew = nCtx - nPrompt
	}
	if maxNew <= 0 {
		return text, fmt.Errorf("s1: transcript too long (%d tokens)", nPrompt)
	}

	// Each call is independent: drop whatever the previous one left in the KV
	// cache and decode this prompt from position 0.
	C.llama_memory_clear(C.llama_get_memory(c.lctx), true)

	batch := C.llama_batch_get_one(&toks[0], C.int32_t(nPrompt))
	var out strings.Builder
	var piece [256]C.char
	for i := 0; i < maxNew; i++ {
		if rc := C.llama_decode(c.lctx, batch); rc != 0 {
			return text, fmt.Errorf("s1: decode failed (%d)", rc)
		}
		tok := C.llama_sampler_sample(c.smpl, c.lctx, -1)
		if C.llama_vocab_is_eog(c.vocab, tok) {
			break
		}
		pn := C.llama_token_to_piece(c.vocab, tok, &piece[0], C.int32_t(len(piece)), 0, true)
		if pn > 0 {
			out.WriteString(C.GoStringN(&piece[0], pn))
		}
		toks[0] = tok
		batch = C.llama_batch_get_one(&toks[0], 1)
	}

	cleaned := strings.TrimSpace(out.String())
	if cleaned == "" {
		return text, fmt.Errorf("s1: empty output")
	}
	return cleaned, nil
}

// Close frees the model. Safe to call once; waits for an in-flight Normalize.
func (c *Ctx) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lctx == nil {
		return
	}
	C.llama_sampler_free(c.smpl)
	C.llama_free(c.lctx)
	C.llama_model_free(c.model)
	c.smpl, c.lctx, c.model, c.vocab = nil, nil, nil, nil
}
