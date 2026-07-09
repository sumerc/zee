package transcriber

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"time"

	"zee/audio"
	"zee/encoder"
	"zee/internal/parakeet"
	"zee/localmodel"
)

// Parakeet is the offline, on-device provider. It wraps one loaded GGUF model
// (decision #2: a single shared ctx; push-to-talk is serial) and swaps the
// gguf to change models (decision #3: 110m loaded at startup, switching freezes
// briefly). The C-API has no usable language parameter for these models, so
// language is model-driven (decision #1): English-only for 110m/v2, and the
// multilingual v3 auto-detects (it is not prompt-conditioned, so a target
// language cannot be forced).
type Parakeet struct {
	mu      sync.Mutex
	modelID string
	ctx     *parakeet.Ctx
	loadErr error
	lang    string
}

// parakeetAvailable reports whether local transcription is compiled in and the
// default model is on disk — the gate for the no-key fallback.
func parakeetAvailable() bool {
	return parakeet.Available() && localmodel.Present(localmodel.Default())
}

// parakeetProvider is the registry entry: availability is "default model on
// disk", status is per-gguf presence, and missing models are downloadable.
func parakeetProvider() ProviderInfo {
	return ProviderInfo{
		Name:      "parakeet",
		Label:     "Local (Parakeet)",
		Models:    ParakeetModels(),
		Available: parakeetAvailable,
		New:       func() Transcriber { return NewParakeet() },
		Status: func(id string) ModelStatus {
			m, ok := localmodel.ByID(id)
			if !ok || !parakeet.Available() {
				return ModelStatus{} // unknown, or not compiled in → Unavailable
			}
			if localmodel.Present(m) {
				return ModelStatus{Ready: true}
			}
			return ModelStatus{Downloadable: true, Detail: humanSize(m.SizeBytes)}
		},
		Download: func(id string, progress func(float64)) error {
			m, ok := localmodel.ByID(id)
			if !ok {
				return fmt.Errorf("unknown local model %q", id)
			}
			return localmodel.Download(m, progress)
		},
	}
}

// ParakeetModels lists the on-device models as ModelInfo (for the tray) without
// needing a loaded provider instance.
func ParakeetModels() []ModelInfo {
	out := make([]ModelInfo, 0, len(localmodel.All()))
	for _, m := range localmodel.All() {
		out = append(out, ModelInfo{ID: m.ID, Label: m.Label, Stream: false, Languages: parakeetLanguages(m)})
	}
	return out
}

func humanSize(b int64) string {
	if b >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	}
	return fmt.Sprintf("%d MB", b>>20)
}

// NewParakeet builds the provider and eagerly loads the default model (110m,
// ~55 ms) so the first recording is instant. A missing/failed model is deferred
// to NewSession as an error.
func NewParakeet() *Parakeet {
	p := &Parakeet{modelID: localmodel.ID110mEN, lang: "en"}
	p.mu.Lock()
	p.load()
	p.mu.Unlock()
	return p
}

// load (mu held) swaps the loaded ctx to p.modelID, freeing the previous one.
func (p *Parakeet) load() {
	m, ok := localmodel.ByID(p.modelID)
	if !ok {
		p.loadErr = fmt.Errorf("unknown local model %q", p.modelID)
		return
	}
	if !localmodel.Present(m) {
		p.loadErr = fmt.Errorf("model %q not downloaded", m.Label)
		return
	}
	if p.ctx != nil {
		p.ctx.Close()
		p.ctx = nil
	}
	ctx, err := parakeet.New(localmodel.Path(m))
	p.ctx, p.loadErr = ctx, err
}

// IsLocal reports whether tr is the on-device provider. Local decode has no
// hint biasing, no streaming, and no audio encoding, so the UI greys those out.
func IsLocal(tr Transcriber) bool {
	_, ok := tr.(*Parakeet)
	return ok
}

func (p *Parakeet) Name() string { return "parakeet" }

func (p *Parakeet) Models() []ModelInfo { return ParakeetModels() }

func parakeetLanguages(m localmodel.Model) []Language {
	if m.Multilingual {
		// v3 auto-detects and is not prompt-conditioned, so a target language
		// cannot be forced — Auto-detect is the only meaningful option.
		return []Language{{Code: "", Label: "Auto-detect"}}
	}
	return []Language{{Code: "en", Label: "English"}}
}

func (p *Parakeet) SupportedLanguages() []Language {
	if m, ok := localmodel.ByID(p.GetModel()); ok {
		return parakeetLanguages(m)
	}
	return []Language{{Code: "en", Label: "English"}}
}

func (p *Parakeet) SetLanguage(lang string) { p.mu.Lock(); p.lang = lang; p.mu.Unlock() }
func (p *Parakeet) GetLanguage() string     { p.mu.Lock(); defer p.mu.Unlock(); return p.lang }

func (p *Parakeet) GetModel() string { p.mu.Lock(); defer p.mu.Unlock(); return p.modelID }

// SetModel swaps the active model, loading its gguf eagerly. The caller (tray)
// is intentionally blocked during the load (decision #3); a load failure is
// surfaced at NewSession.
func (p *Parakeet) SetModel(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id == p.modelID && p.ctx != nil {
		return
	}
	p.modelID = id
	p.load()
}

func (p *Parakeet) NewSession(_ context.Context, cfg SessionConfig) (Session, error) {
	if cfg.Stream {
		return nil, fmt.Errorf("parakeet does not support streaming")
	}
	p.mu.Lock()
	if p.ctx == nil && p.loadErr == nil {
		p.load()
	}
	ctx, err := p.ctx, p.loadErr
	decoder := 0
	if m, ok := localmodel.ByID(p.modelID); ok {
		decoder = m.Decoder
	}
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &pcmSession{ctx: ctx, decoder: decoder, updates: make(chan string)}, nil
}

// Close frees the loaded model. The provider is reusable afterwards (the next
// NewSession reloads lazily).
func (p *Parakeet) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx != nil {
		p.ctx.Close()
		p.ctx = nil
	}
}

// pcmSession buffers raw S16LE PCM during recording, then runs one batch
// inference on Close. Same Session interface as the cloud batch path, so the
// live hotkey and -transcribe share it — no encoder, no network.
type pcmSession struct {
	ctx     *parakeet.Ctx
	decoder int
	mu      sync.Mutex
	pcm     []byte
	updates chan string
}

func (s *pcmSession) Feed(pcm []byte) {
	s.mu.Lock()
	s.pcm = append(s.pcm, pcm...)
	s.mu.Unlock()
}

func (s *pcmSession) Updates() <-chan string { return s.updates }

func (s *pcmSession) Close() (SessionResult, error) {
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

	start := time.Now()
	text, err := s.ctx.Transcribe(f32, s.decoder)
	if err != nil {
		return SessionResult{}, err
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
		AudioData:   audio.PCMToWAV(raw),
		AudioFormat: "wav",
		Batch: &BatchStats{
			AudioLengthS: audioSec,
			RawSizeKB:    rawKB,
			InferenceMs:  inferenceMs,
			TotalTimeMs:  inferenceMs,
		},
		Metrics: []string{
			fmt.Sprintf("audio:      %.1fs | %.1f KB (raw PCM, no encoding)", audioSec, rawKB),
			fmt.Sprintf("inference:  %.0fms (local, CPU)", inferenceMs),
			fmt.Sprintf("rtfx:       %.1fx", audioSec/(inferenceMs/1000)),
		},
	}
	sr.captureRSS()
	return sr, nil
}
