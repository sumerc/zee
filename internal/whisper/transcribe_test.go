//go:build darwin && arm64

package whisper_test

import (
	"os"
	"strings"
	"testing"

	"zee/audio"
	"zee/internal/whisper"
	"zee/localmodel"
)

// TestTranscribeKnownClips is a correctness guard, not a benchmark. It exists
// because a wrong whisper_full parameter does not fail — it returns fluent
// garbage. A dynamic audio_ctx turned "the quick brown fox jumps" into ",, I'm"
// while still passing every build, vet and timing check, so the only way to
// catch that class of bug is to assert on the words themselves.
//
// Skips when the model isn't downloaded, so a fresh checkout never fails.
func TestTranscribeKnownClips(t *testing.T) {
	m, ok := localmodel.ByID(localmodel.IDWhisperQ5)
	if !ok {
		t.Fatal("whisper model missing from the registry")
	}
	if !localmodel.Present(m) {
		t.Skipf("%s not downloaded (run make download-models)", m.ID)
	}

	ctx, err := whisper.New(localmodel.Path(m))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer ctx.Close()

	for _, tc := range []struct {
		clip  string
		words []string
	}{
		{"../../test/data/short.wav", []string{"testing"}},
		{"../../test/data/en.wav", []string{"quick", "brown", "fox"}},
	} {
		t.Run(tc.clip, func(t *testing.T) {
			raw, err := os.ReadFile(tc.clip)
			if err != nil {
				t.Skipf("read: %v", err)
			}
			pcm, err := audio.WAVToPCM(raw)
			if err != nil {
				t.Skipf("decode: %v", err)
			}
			got, err := ctx.Transcribe(audio.PCMToF32(pcm), "en")
			if err != nil {
				t.Fatalf("transcribe: %v", err)
			}
			lower := strings.ToLower(got)
			for _, w := range tc.words {
				if !strings.Contains(lower, w) {
					t.Errorf("transcript %q missing %q — whisper is producing garbage; "+
						"check the whisper_full params (audio_ctx especially)", got, w)
				}
			}
		})
	}
}
