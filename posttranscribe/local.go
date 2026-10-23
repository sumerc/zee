package posttranscribe

import (
	"fmt"
	"sync"

	"zee/internal/llama"
	"zee/localmodel"
)

// maxLocalTokens bounds the local reply. A rewrite is about as long as the
// dictation, so this leaves room for a long one inside llama's context.
const maxLocalTokens = 1024

// The local model is loaded on first use and kept resident: loading it maps
// the whole GGUF and uploads it to the GPU, which costs more than a rewrite.
var (
	localMu    sync.Mutex
	loaded     *llama.Model
	loadedPath string
)

// localEntry returns the registry entry when model names a local LLM
// (config.json's post_transcribe.model set to its ID, e.g. "gemma-4-e2b").
func localEntry(model string) (localmodel.Model, bool) {
	m, ok := localmodel.ByID(model)
	return m, ok && m.Engine == localmodel.EngineLlama
}

// loadLocal returns m loaded, loading it if it is not the resident model.
func loadLocal(m localmodel.Model) (*llama.Model, error) {
	localMu.Lock()
	defer localMu.Unlock()
	path := localmodel.Path(m)
	if loaded != nil && loadedPath == path {
		return loaded, nil
	}
	if !localmodel.Present(m) {
		return nil, fmt.Errorf("%s is not downloaded: place %s in %s", m.ID, m.Filename, localmodel.Dir())
	}
	if loaded != nil { // config.json switched to another local model
		loaded.Close()
		loaded = nil
	}
	l, err := llama.Load(path)
	if err != nil {
		return nil, err
	}
	loaded, loadedPath = l, path
	return l, nil
}

// completeLocal runs one rewrite on the local model. It has no timeout: a
// llama generation cannot be cut short, and maxLocalTokens bounds it instead.
func completeLocal(m localmodel.Model, system, text string) (string, error) {
	l, err := loadLocal(m)
	if err != nil {
		return "", err
	}
	out, err := l.Generate(gemma4Prompt(system, userMessage(text)), maxLocalTokens)
	if err != nil {
		return "", err
	}
	return cleanReply(m.ID, out)
}

// gemma4Prompt renders one system + user turn in Gemma 4's chat format, with
// thinking off (no <|think|> line). llama.cpp's C API does not know this
// template at the pinned commit, so it is written out here; every EngineLlama
// model in the registry is a Gemma 4.
func gemma4Prompt(system, user string) string {
	return "<|turn>system\n" + system + "<turn|>\n<|turn>user\n" + user + "<turn|>\n<|turn>model\n"
}
