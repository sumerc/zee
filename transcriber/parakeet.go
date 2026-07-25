package transcriber

import (
	"zee/internal/parakeet"
	"zee/localmodel"
)

// Parakeet is the on-device English fast path. The C-API has no language
// parameter for these models, so language is model-driven (decision #1): each
// gguf is built for one language and the setting is ignored.
//
// Everything about loading, swapping and running a local model lives in
// localProvider (local.go); this file is only the registry entry plus the
// adapter that satisfies localEngine.

// parakeetEngine adapts a loaded gguf to localEngine. The decoder head is fixed
// per model, so it is captured here rather than threaded through the shared
// code; lang and hints are ignored because the C-API has nowhere to put them —
// a greedy CTC/TDT decode has no prompt to bias.
type parakeetEngine struct {
	ctx     *parakeet.Ctx
	decoder int
}

func (e parakeetEngine) Transcribe(pcm []float32, _, _ string) (string, error) {
	return e.ctx.Transcribe(pcm, e.decoder)
}

func (e parakeetEngine) Close() { e.ctx.Close() }

func openParakeet(m localmodel.Model) (localEngine, error) {
	ctx, err := parakeet.New(localmodel.Path(m))
	if err != nil {
		return nil, err
	}
	return parakeetEngine{ctx: ctx, decoder: m.Decoder}, nil
}

// parakeetLanguages: these models are single-language by build, so the menu
// offers exactly the one they were trained for.
func parakeetLanguages(m localmodel.Model) []Language {
	if m.Multilingual {
		return []Language{{Code: "", Label: "Auto-detect"}}
	}
	return []Language{{Code: "en", Label: "English"}}
}

func parakeetProvider() ProviderInfo {
	return localProviderInfo(
		localmodel.EngineParakeet, "Local (Parakeet)",
		localmodel.ID110mEN, "en",
		parakeet.Available(), false, // hints: no prompt surface in a greedy decode
		openParakeet, parakeetLanguages,
	)
}

// ParakeetModels lists the on-device Parakeet models as ModelInfo (for the tray)
// without needing a loaded provider instance.
func ParakeetModels() []ModelInfo {
	return modelInfos(localModels(localmodel.EngineParakeet), parakeetLanguages)
}
