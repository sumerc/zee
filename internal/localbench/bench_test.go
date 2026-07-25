//go:build darwin && arm64

package localbench_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"zee/audio"
	"zee/internal/parakeet"
	"zee/internal/whisper"
	"zee/localmodel"
)

// Benchmarks the on-device engines directly — model load + Transcribe, with no
// capture, encoder, network or CLI in the way. This is the isolated-inference
// counterpart to the end-to-end `zee -benchmark` flow. Every registry model is
// covered, Parakeet and Whisper alike, each loaded by its own backend.
//
//	make bench-local                      # bundled test/data/short.wav
//	make bench-local WAV=~/clips/a.wav    # one custom clip
//	make bench-local WAV=~/Library/Application\ Support/zee/samples   # a whole dir
//
// Clips come from ZEE_BENCH_WAV (a file or a directory scanned for *.wav) and
// must be 16 kHz mono 16-bit — WAVToPCM rejects anything else, and such a clip
// is skipped rather than benchmarked wrong. Saved samples from the local engine
// are already in that format; ones saved from a cloud provider are .mp3 and are
// ignored by the *.wav scan.
//
// Each (clip, model) pair is its own sub-benchmark — BenchmarkTranscribe/<clip>/<model>
// — so `benchstat old.txt new.txt` lines the same clip up across parakeet.cpp or
// quantization changes. Models absent from disk are skipped, so this never fails
// a machine that hasn't run `make download-models`.

const defaultBenchWAV = "../../test/data/short.wav"

// benchClips resolves ZEE_BENCH_WAV to a list of wav paths (a file as-is, a
// directory scanned one level for *.wav).
func benchClips(tb testing.TB) []string {
	src := os.Getenv("ZEE_BENCH_WAV")
	if src == "" {
		src = defaultBenchWAV
	}
	fi, err := os.Stat(src)
	if err != nil {
		tb.Skipf("ZEE_BENCH_WAV %q: %v", src, err)
	}
	if !fi.IsDir() {
		return []string{src}
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		tb.Skipf("read dir %q: %v", src, err)
	}
	var clips []string
	for _, e := range entries {
		if e.IsDir() {
			// Saved samples live one level down: <samples>/<timestamp>/audio.wav.
			nested := filepath.Join(src, e.Name(), "audio.wav")
			if _, err := os.Stat(nested); err == nil {
				clips = append(clips, nested)
			}
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".wav") {
			clips = append(clips, filepath.Join(src, e.Name()))
		}
	}
	sort.Strings(clips) // stable sub-benchmark order across runs
	if len(clips) == 0 {
		tb.Skipf("no .wav clips under %q", src)
	}
	return clips
}

// clipName labels a sub-benchmark. Saved samples are all named audio.wav, so
// those are labelled by their timestamp directory instead.
func clipName(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if base == "audio" {
		base = filepath.Base(filepath.Dir(path))
	}
	return strings.ReplaceAll(base, " ", "_") // spaces break benchstat parsing
}

// loadPCM reads a wav and converts it to the engine's float32 samples. Format
// validation is WAVToPCM's (16 kHz mono 16-bit); anything else skips the clip.
func loadPCM(tb testing.TB, path string) []float32 {
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Skipf("read %s: %v", path, err)
	}
	pcm, err := audio.WAVToPCM(raw)
	if err != nil {
		tb.Skipf("%s: %v", filepath.Base(path), err)
	}
	if len(pcm) == 0 {
		tb.Skipf("%s: empty", filepath.Base(path))
	}
	return audio.PCMToF32(pcm)
}

// transcribeFn runs one inference at the given language. Parakeet ignores lang
// (its C-API has no such parameter); whisper honours it.
type transcribeFn func(pcm []float32, lang string) (string, error)

// openEngine loads a model with whichever backend owns it, returning a uniform
// transcribe closure plus a cleanup, so the timing loop below is identical for
// both engines. New() warms up in both cases, so every timed run is steady state.
func openEngine(b *testing.B, m localmodel.Model) (transcribeFn, func()) {
	b.Helper()
	switch m.Engine {
	case localmodel.EngineWhisper:
		ctx, err := whisper.New(localmodel.Path(m))
		if err != nil {
			b.Fatalf("load %s: %v", m.ID, err)
		}
		return ctx.Transcribe, ctx.Close
	default:
		ctx, err := parakeet.New(localmodel.Path(m))
		if err != nil {
			b.Fatalf("load %s: %v", m.ID, err)
		}
		return func(pcm []float32, _ string) (string, error) {
			return ctx.Transcribe(pcm, m.Decoder)
		}, ctx.Close
	}
}

// variant is one language mode to measure for a model.
type variant struct {
	label string // "" = no extra sub-benchmark level
	lang  string
}

// variantsFor decides which language modes to time. Parakeet has exactly one.
// Whisper is measured twice on purpose: "auto" is what actually ships (and pays
// a second encoder pass to detect the language), while "en" is the only number
// comparable with parakeet's single pass. Reporting just one of them would
// either hide the shipped cost or make the engines look falsely far apart.
func variantsFor(m localmodel.Model) []variant {
	if m.Engine == localmodel.EngineWhisper {
		return []variant{{"auto", ""}, {"en", "en"}}
	}
	return []variant{{"", ""}}
}

func BenchmarkTranscribe(b *testing.B) {
	clips := benchClips(b)

	for _, m := range localmodel.All() {
		if !localmodel.Present(m) {
			b.Logf("skipping %s: not downloaded (run make download-models)", m.ID)
			continue
		}
		b.Run(m.ID, func(b *testing.B) {
			transcribe, closeEngine := openEngine(b, m)
			defer closeEngine()

			for _, clip := range clips {
				for _, v := range variantsFor(m) {
					name := clipName(clip)
					if v.label != "" {
						name += "/" + v.label
					}
					b.Run(name, func(b *testing.B) {
						// Loaded inside the sub-benchmark so an unusable clip skips
						// only itself, and reset below so decode isn't timed.
						pcm := loadPCM(b, clip)
						audioSec := float64(len(pcm)) / 16000
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							if _, err := transcribe(pcm, v.lang); err != nil {
								b.Fatalf("transcribe: %v", err)
							}
						}
						b.StopTimer()
						// Realtime factor: audio seconds processed per wall second.
						// Higher is faster; comparable across clips of any length.
						secPerOp := b.Elapsed().Seconds() / float64(b.N)
						b.ReportMetric(audioSec/secPerOp, "xRT")
						b.ReportMetric(audioSec, "audio_s")
					})
				}
			}
		})
	}
}
