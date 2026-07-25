package transcriber

import (
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"time"

	"zee/audio"
	"zee/encoder"
)

// localSession buffers raw S16LE PCM during recording, then runs one batch
// inference on Close. Same Session interface as the cloud batch path, so the
// live hotkey and -transcribe share it — no encoder, no network.
type localSession struct {
	engine  localEngine
	lang    string
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

	n := len(raw) / 2
	if n == 0 {
		return SessionResult{NoSpeech: true}, nil
	}

	f32 := make([]float32, n)
	for i := 0; i < n; i++ {
		f32[i] = float32(int16(binary.LittleEndian.Uint16(raw[i*2:]))) / 32768.0
	}

	audioData := audio.PCMToWAV(raw)

	start := time.Now()
	text, err := s.engine.Transcribe(f32, s.lang)
	if err != nil {
		return SessionResult{AudioData: audioData, AudioFormat: "wav"}, err
	}
	inferenceMs := float64(time.Since(start).Microseconds()) / 1000

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
