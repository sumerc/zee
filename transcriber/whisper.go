package transcriber

import (
	"zee/internal/whisper"
	"zee/localmodel"
)

// Whisper is the on-device multilingual provider. Unlike Parakeet it takes a
// language per transcription, so the tray's language menu is live for it.
//
// It defaults to auto-detect ("") rather than a fixed language. That is a
// measured choice, not a preference: whisper's language setting hard-forces the
// start-of-transcript token, so dictating Turkish while the model is pinned to
// English does not merely mislabel the output — it garbles it. Auto-detect
// costs one extra encoder pass (~250 ms on M5) and is the only mode that
// survives code-switching mid-sentence.

// whisperEngine adapts a loaded ggml model to localEngine.
type whisperEngine struct{ ctx *whisper.Ctx }

func (e whisperEngine) Transcribe(pcm []float32, lang string) (string, error) {
	return e.ctx.Transcribe(pcm, lang)
}

func (e whisperEngine) Close() { e.ctx.Close() }

func openWhisper(m localmodel.Model) (localEngine, error) {
	ctx, err := whisper.New(localmodel.Path(m))
	if err != nil {
		return nil, err
	}
	return whisperEngine{ctx: ctx}, nil
}

// whisperLanguages: the turbo model is multilingual, so the full language
// universe applies, with Auto-detect first.
func whisperLanguages(localmodel.Model) []Language { return AllLanguages() }

func whisperProvider() ProviderInfo {
	return localProviderInfo(
		localmodel.EngineWhisper, "Local (Whisper)",
		localmodel.IDWhisperQ5, "", // "" = auto-detect
		whisper.Available(),
		openWhisper, whisperLanguages,
	)
}
