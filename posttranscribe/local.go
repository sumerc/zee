package posttranscribe

import (
	"fmt"
	"sync"

	"zee/internal/llama"
	"zee/localmodel"
)

// maxLocalTokens bounds the local reply, so a model that does not stop holds
// the recorder only so long. A rewrite is about as long as the dictation, so
// this leaves room for a long one; llama also stops at the end of its context.
// A reply cut off at either limit is an error: the raw transcript is pasted.
const maxLocalTokens = 1024

// The local model is loaded on first use and kept resident: loading it maps
// the whole GGUF and uploads it to the GPU, which costs more than a rewrite.
var (
	localMu    sync.Mutex
	loaded     *llama.Model
	loadedPath string
	// unloads tracks Release's background unloads, so a test can wait for them.
	unloads sync.WaitGroup
)

// localEntry returns the registry entry when model names a local LLM.
func localEntry(model string) (localmodel.Model, bool) {
	m, ok := localmodel.ByID(model)
	return m, ok && m.Engine == localmodel.EngineLlama
}

// localIDs lists the local LLMs, for error messages.
func localIDs() []string {
	var ids []string
	for _, m := range localmodel.All() {
		if m.Engine == localmodel.EngineLlama {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

// loadLocal returns m loaded, loading it if it is not the resident model.
func loadLocal(m localmodel.Model) (*llama.Model, error) {
	localMu.Lock()
	defer localMu.Unlock()
	path := localmodel.Path(m)
	if loaded != nil && loadedPath == path {
		return loaded, nil
	}
	if !llama.Available() {
		return nil, fmt.Errorf("post_transcribe provider %q needs a Mac with Apple Silicon", ProviderLocal)
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

// unloadLocal frees the resident model, if any. It waits for a running
// loadLocal and Close waits for a running Generate, so Release runs it in the
// background.
func unloadLocal() {
	localMu.Lock()
	l := loaded
	loaded, loadedPath = nil, ""
	localMu.Unlock()
	if l != nil {
		l.Close()
	}
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
