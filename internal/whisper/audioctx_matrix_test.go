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

// Fault matrix for the reduced-audio_ctx garbling. Each case is a sequence of
// calls on ONE cold context (no warm-up), isolating one variable: shape change
// (audio_ctx differing between calls), content (silence vs speech), and
// language mode (auto vs fixed). Diagnostic — run with ZEE_AC_DEBUG=1:
//
//	ZEE_AC_DEBUG=1 ZEE_MODELS_DIR=... go test ./internal/whisper -run FaultMatrix -v
func TestAudioCtxFaultMatrix(t *testing.T) {
	if os.Getenv("ZEE_AC_DEBUG") == "" {
		t.Skip("diagnostic matrix; set ZEE_AC_DEBUG=1 to run")
	}
	m, ok := localmodel.ByID(localmodel.IDWhisperQ5)
	if !ok || !localmodel.Present(m) {
		t.Skip("whisper model not downloaded")
	}

	load := func(path string) []float32 {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		pcm, err := audio.WAVToPCM(raw)
		if err != nil {
			t.Fatal(err)
		}
		return audio.PCMToF32(pcm)
	}
	clips := map[string][]float32{
		"short":   load("../../test/data/short.wav"), // "Testing 1 2 3"
		"en":      load("../../test/data/en.wav"),    // "the quick brown fox jumps"
		"silence": make([]float32, 16000),            // the shipped warm-up input
	}

	type step struct {
		clip string
		lang string
		ac   int
	}
	cases := []struct {
		name  string
		steps []step
		want  string // must appear in the LAST step's transcript
	}{
		{"A_baseline_sized", []step{{"en", "en", 400}}, "fox"},
		{"B_same_shape_x3", []step{{"en", "en", 400}, {"en", "en", 400}, {"en", "en", 400}}, "fox"},
		{"C_const_shape_two_clips", []step{{"short", "en", 400}, {"en", "en", 400}}, "fox"},
		{"D_shape_change_full_then_sized", []step{{"en", "en", 0}, {"en", "en", 400}}, "fox"},
		{"E_silence_then_sized_const_shape", []step{{"silence", "en", 400}, {"en", "en", 400}}, "fox"},
		{"F_shipped_warmup_repro", []step{{"silence", "auto", 0}, {"en", "en", 400}}, "fox"},
		{"G_silence_full_then_sized_en", []step{{"silence", "en", 0}, {"en", "en", 400}}, "fox"},
		{"H_auto_sized_fresh", []step{{"en", "auto", 400}}, "fox"},
		{"I_recovery_sized_full_sized", []step{{"en", "en", 400}, {"en", "en", 0}, {"en", "en", 400}}, "fox"},
		{"J_shrink_sized_to_sized", []step{{"en", "en", 400}, {"en", "en", 200}}, "fox"},
		{"K_grow_sized_to_sized", []step{{"en", "en", 200}, {"en", "en", 400}}, "fox"},
		{"L_explicit_1500_then_sized", []step{{"en", "en", 1500}, {"en", "en", 400}}, "fox"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, err := whisper.NewNoWarm(localmodel.Path(m))
			if err != nil {
				t.Fatal(err)
			}
			defer ctx.Close()
			var last string
			for i, s := range tc.steps {
				out, err := ctx.TranscribeAt(clips[s.clip], s.lang, s.ac)
				if err != nil {
					t.Fatalf("step %d: %v", i+1, err)
				}
				t.Logf("step %d  %-8s lang=%-4s ac=%-4d => %q", i+1, s.clip, s.lang, s.ac, out)
				last = out
			}
			if !strings.Contains(strings.ToLower(last), tc.want) {
				t.Errorf("FAIL: last transcript %q missing %q", last, tc.want)
			}
		})
	}
}
