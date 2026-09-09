package transcriber

import (
	"zee/internal/whisper"
	"zee/localmodel"
)

// Whisper is the on-device multilingual provider. Unlike Parakeet it takes a
// language per transcription, so the tray's language menu is live for it.
//
// It defaults to "en", not auto-detect, even though the model is multilingual.
// Auto-detect reads only the first 30 s and commits that language to the whole
// recording, and when it guesses wrong the output is not mislabelled — the
// language token conditions the decoder, so the audio comes back *translated*:
// fluent, on-topic, wrong language. Measured on real dictation, detection
// misfires often enough on quiet or accented speech to matter, and the failure
// modes are asymmetric — a forced "en" still yields readable text for Turkish
// speech, while auto can hand back Turkish for English and force a redo.
// Auto stays selectable in the menu for genuinely unknown audio.
// See docs/design-notes.md, "Why English is the default language for every
// model".

// whisperEngine adapts a loaded ggml model to localEngine.
type whisperEngine struct{ ctx *whisper.Ctx }

// hints become initial_prompt, whose language can override lang — a hazard,
// deliberately unfixed. See design-notes, "Known: bare-list hints flip the
// transcription language".
func (e whisperEngine) Transcribe(pcm []float32, lang, hints string) (string, error) {
	return e.ctx.Transcribe(pcm, lang, hints)
}

// LastDetection satisfies the optional interface localSession probes to log
// what auto-detect chose. Whisper is the only engine that detects a language.
func (e whisperEngine) LastDetection() (string, float64) { return e.ctx.LastDetection() }

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
		localmodel.IDWhisperQ5, "en", // multilingual, but English by default — see above
		whisper.Available(), true, // hints: fed in as whisper's initial prompt
		openWhisper, whisperLanguages,
	)
}
