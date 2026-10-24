//go:build darwin && arm64

// Package llama is a thin cgo wrapper over llama.cpp's C API. It loads a GGUF
// chat model and completes an already-formatted prompt greedily, stopping at
// the model's end-of-generation token. llama.cpp is built on the same ggml as
// whisper and parakeet (make llama-lib), so the process carries one ggml.
//
// A Model is safe for concurrent use: calls are serialized, since a llama
// context holds one KV cache.
package llama

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
#include <string.h>
#include "llama.h"

static void zee_llama_quiet(enum ggml_log_level level, const char *text, void *user) {
	(void)level; (void)text; (void)user;
}

static void zee_llama_init(void) {
	llama_log_set(zee_llama_quiet, NULL);
	llama_backend_init();
}

static struct llama_model *zee_llama_load(const char *path) {
	struct llama_model_params mp = llama_model_default_params();
	mp.n_gpu_layers = 999; // every layer on Metal
	return llama_model_load_from_file(path, mp);
}

static struct llama_context *zee_llama_context(struct llama_model *m, int n_ctx) {
	struct llama_context_params cp = llama_context_default_params();
	cp.n_ctx = n_ctx;
	cp.n_batch = n_ctx; // the whole prompt goes in one decode
	cp.no_perf = true;
	return llama_init_from_model(m, cp);
}

// zee_llama_generate decodes prompt and samples greedily until an
// end-of-generation token or max_tokens. It writes the reply (special tokens
// not rendered) into out and returns its length, or a negative error:
// -1 tokenize, -2 prompt too long, -3 decode, -4 out of memory.
static int zee_llama_generate(struct llama_model *m, struct llama_context *ctx,
                              const char *prompt, int max_tokens, char *out, int out_cap) {
	const struct llama_vocab *vocab = llama_model_get_vocab(m);
	llama_memory_clear(llama_get_memory(ctx), true);

	int n = -llama_tokenize(vocab, prompt, (int)strlen(prompt), NULL, 0, true, true);
	if (n <= 0) return -1;
	if (n >= (int)llama_n_ctx(ctx) - max_tokens) return -2;
	llama_token *toks = malloc(sizeof(llama_token) * n);
	if (!toks) return -4;
	if (llama_tokenize(vocab, prompt, (int)strlen(prompt), toks, n, true, true) < 0) {
		free(toks);
		return -1;
	}
	int rc = llama_decode(ctx, llama_batch_get_one(toks, n));
	free(toks);
	if (rc != 0) return -3;

	struct llama_sampler *smpl = llama_sampler_chain_init(llama_sampler_chain_default_params());
	llama_sampler_chain_add(smpl, llama_sampler_init_greedy());
	int len = 0;
	for (int i = 0; i < max_tokens; i++) {
		llama_token t = llama_sampler_sample(smpl, ctx, -1);
		if (llama_vocab_is_eog(vocab, t)) break;
		char piece[256];
		int k = llama_token_to_piece(vocab, t, piece, sizeof(piece), 0, false);
		if (k > 0 && len + k < out_cap) {
			memcpy(out + len, piece, k);
			len += k;
		}
		if (llama_decode(ctx, llama_batch_get_one(&t, 1)) != 0) {
			llama_sampler_free(smpl);
			return -3;
		}
	}
	llama_sampler_free(smpl);
	return len;
}
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

// Available reports whether local LLM inference is compiled in.
func Available() bool { return true }

// contextTokens bounds prompt + reply. A post-transcribe prompt (rules, hints,
// a dictation) plus its rewrite fits with room to spare.
const contextTokens = 4096

var initOnce sync.Once

// Model is a loaded GGUF chat model with one context.
type Model struct {
	mu    sync.Mutex
	model *C.struct_llama_model
	ctx   *C.struct_llama_context
}

// Load reads a GGUF model and creates its context.
func Load(path string) (*Model, error) {
	initOnce.Do(func() { C.zee_llama_init() })
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	m := C.zee_llama_load(cpath)
	if m == nil {
		return nil, fmt.Errorf("llama: cannot load %s", path)
	}
	ctx := C.zee_llama_context(m, contextTokens)
	if ctx == nil {
		C.llama_model_free(m)
		return nil, fmt.Errorf("llama: cannot create a context for %s", path)
	}
	return &Model{model: m, ctx: ctx}, nil
}

// Generate completes prompt (already in the model's chat format) and returns
// the reply, at most maxTokens long.
func (m *Model) Generate(prompt string, maxTokens int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx == nil {
		return "", fmt.Errorf("llama: model is closed")
	}
	cprompt := C.CString(prompt)
	defer C.free(unsafe.Pointer(cprompt))
	capBytes := maxTokens*32 + 1
	buf := (*C.char)(C.malloc(C.size_t(capBytes)))
	defer C.free(unsafe.Pointer(buf))
	n := C.zee_llama_generate(m.model, m.ctx, cprompt, C.int(maxTokens), buf, C.int(capBytes))
	switch {
	case n == -2:
		return "", fmt.Errorf("llama: prompt too long for a %d-token context", contextTokens)
	case n < 0:
		return "", fmt.Errorf("llama: generation failed (%d)", int(n))
	}
	return C.GoStringN(buf, n), nil
}

// Close frees the context and the model.
func (m *Model) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx != nil {
		C.llama_free(m.ctx)
		C.llama_model_free(m.model)
		m.ctx, m.model = nil, nil
	}
}
