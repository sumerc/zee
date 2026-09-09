package transcriber

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"zee/audio"
	"zee/encoder"
	"zee/log"
)

// logDetectedLanguage records what auto-detect chose, for engines that detect
// at all (whisper; parakeet's models are fixed-language and report nothing).
//
// It is a bare diagnostic, deliberately: a mis-detection produces a fluent
// transcript in the wrong language, which looks like a transcription bug rather
// than a detection one, and the log is the only place the difference shows.
// Measured on real dictation, a correct call and a wrong one are NOT separated
// by the winner's probability — see docs/design-notes.md — so the runner-up
// matters too and is worth having on the record when it becomes available.
func logDetectedLanguage(e localEngine) {
	d, ok := e.(interface{ LastDetection() (string, float64) })
	if !ok {
		return
	}
	lang, p := d.LastDetection()
	if lang == "" {
		return // language was forced; nothing was detected
	}
	log.Info(fmt.Sprintf("lang_detect lang=%s p=%.4f", lang, p))
}

// localSession buffers raw S16LE PCM during recording, then runs one batch
// inference on Close. Same Session interface as the cloud batch path, so the
// live hotkey and -transcribe share it — no encoder, no network.
type localSession struct {
	engine  localEngine
	lang    string
	hints   string
	mu      sync.Mutex
	pcm     []byte
	updates chan string
}

func (s *localSession) Feed(pcm []byte) {
	s.mu.Lock()
	s.pcm = append(s.pcm, pcm...)
	s.mu.Unlock()
}

func (s *localSession) Updates() <-chan string { return s.updates }

func (s *localSession) Close() (SessionResult, error) {
	close(s.updates)

	s.mu.Lock()
	raw := s.pcm
	s.mu.Unlock()

	convStart := time.Now()
	f32 := audio.PCMToF32(raw)
	n := len(f32)
	if n == 0 {
		return SessionResult{NoSpeech: true}, nil
	}

	audioData := audio.PCMToWAV(raw)
	convertMs := float64(time.Since(convStart).Microseconds()) / 1000

	start := time.Now()
	text, err := s.engine.Transcribe(f32, s.lang, s.hints)
	if err != nil {
		return SessionResult{AudioData: audioData, AudioFormat: "wav"}, err
	}
	inferenceMs := float64(time.Since(start).Microseconds()) / 1000
	logDetectedLanguage(s.engine)

	text = strings.TrimSpace(text)
	noSpeech := text == ""
	audioSec := float64(n) / float64(encoder.SampleRate)
	rawKB := float64(len(raw)) / 1024

	sr := SessionResult{
		Text:        text,
		HasText:     !noSpeech,
		NoSpeech:    noSpeech,
		AudioData:   audioData,
		AudioFormat: "wav",
		Batch: &BatchStats{
			AudioLengthS: audioSec,
			RawSizeKB:    rawKB,
			InferenceMs:  inferenceMs,
			ConvertMs:    convertMs,
			TotalTimeMs:  inferenceMs,
		},
		Metrics: []string{
			fmt.Sprintf("audio:      %.1fs | %.1f KB (raw PCM, no encoding)", audioSec, rawKB),
			fmt.Sprintf("inference:  %.0fms (local)", inferenceMs),
			fmt.Sprintf("rtfx:       %.1fx", audioSec/(inferenceMs/1000)),
		},
	}
	sr.captureRSS()
	return sr, nil
}
