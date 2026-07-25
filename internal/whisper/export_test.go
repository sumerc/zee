//go:build darwin && arm64

package whisper

// Test-only exports: the audio_ctx fault matrix needs a cold context (no
// warm-up pass) and direct control of the audio_ctx parameter.

var NewNoWarm = newNoWarm

func (c *Ctx) TranscribeAt(pcm []float32, lang string, audioCtx int) (string, error) {
	return c.transcribeAt(pcm, lang, audioCtx)
}
