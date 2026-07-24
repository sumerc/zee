package transcriber

import (
	"context"
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
// gguf to change models (decision #3: the default is warmed in the background at
// startup, and switches reload in the background too, so the UI never blocks).
// The C-API has no usable language parameter for these models, so language is
// model-driven (decision #1): English-only for 110m/v2, and the multilingual v3
// auto-detects (it is not prompt-conditioned, so a target language cannot be
// forced).
type Parakeet struct {
	mu       sync.Mutex    // guards the fields below; held only briefly, never across a gguf load
	loadMu   sync.Mutex    // serializes load() so concurrent triggers can't double-load
	modelID  string        // desired model
	loadedID string        // model currently in ctx ("" while unloaded or loading)
	ctx      *parakeet.Ctx
	loadErr  error
	lang     string
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
			return ModelStatus{Downloadable: true, Detail: m.HumanSize()}
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

// NewParakeet builds the provider and warms the default model (110m) in the
// background so construction never blocks startup. The load holds only loadMu
// (not mu) while it reads the gguf, so metadata accessors stay responsive; the
// first NewSession waits for it via load()'s idempotent fast path. A missing or
// failed model surfaces at NewSession as an error.
func NewParakeet() *Parakeet {
	p := &Parakeet{modelID: localmodel.ID110mEN, lang: "en"}
	go p.load()
	return p
}

// load brings ctx in line with the desired modelID, freeing the previous one.
// It's serialized by loadMu and idempotent: if the desired model is already
// loaded it returns at once, so NewSession can call it to *wait out* an in-flight
// background load without triggering a reload. The slow gguf read runs with mu
// released, so GetModel/SetLanguage/etc. never block on it.
func (p *Parakeet) load() {
	p.loadMu.Lock()
	defer p.loadMu.Unlock()

	p.mu.Lock()
	want := p.modelID
	if p.ctx != nil && p.loadedID == want {
		p.mu.Unlock()
		return
	}
	old := p.ctx
	p.ctx, p.loadedID, p.loadErr = nil, "", nil
	p.mu.Unlock()

	if old != nil {
		old.Close()
	}

	var (
		ctx *parakeet.Ctx
		err error
	)
	if m, ok := localmodel.ByID(want); !ok {
		err = fmt.Errorf("unknown local model %q", want)
	} else if !localmodel.Present(m) {
		err = fmt.Errorf("model %q not downloaded", m.Label)
	} else {
		ctx, err = parakeet.New(localmodel.Path(m)) // slow; mu released
	}

	p.mu.Lock()
	p.ctx, p.loadErr, p.loadedID = ctx, err, want
	p.mu.Unlock()
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

// SetModel records the desired model and warms it in the background, so a tray
// switch returns immediately instead of blocking the UI on the gguf load. The
// next NewSession waits for the load (the record→inference guard keeps switches
// out of an active cycle, and recording overlaps the load, hiding its latency);
// a load failure surfaces there.
func (p *Parakeet) SetModel(id string) {
	p.mu.Lock()
	same := id == p.modelID
	p.modelID = id
	p.mu.Unlock()
	if same {
		return
	}
	go p.load()
}

// Transcribe decodes a WAV file to PCM and runs one batch inference, satisfying
// the same direct-transcribe interface as the cloud providers so the file path
// (-transcribe) has a single shape. Local decode accepts WAV only and ignores
// hints (greedy decode has no biasing).
func (p *Parakeet) Transcribe(audioData []byte, format, lang, _ string) (*Result, error) {
	if format != "wav" {
		return nil, fmt.Errorf("local transcription supports WAV files only (got %s)", format)
	}
	pcm, err := audio.WAVToPCM(audioData)
	if err != nil {
		return nil, fmt.Errorf("cannot read WAV: %w", err)
	}
	sess, err := p.NewSession(context.Background(), SessionConfig{Language: lang})
	if err != nil {
		return nil, err
	}
	sess.Feed(pcm)
	sr, err := sess.Close()
	if err != nil {
		return nil, err
	}
	res := &Result{Text: sr.Text}
	if sr.Batch != nil {
		res.InferenceMs = sr.Batch.InferenceMs
		res.Duration = sr.Batch.AudioLengthS
	}
	return res, nil
}

func (p *Parakeet) NewSession(_ context.Context, cfg SessionConfig) (Session, error) {
	if cfg.Stream {
		return nil, fmt.Errorf("parakeet does not support streaming")
	}
	p.mu.Lock()
	ready := p.ctx != nil && p.loadedID == p.modelID
	p.mu.Unlock()
	if !ready {
		p.load() // waits out an in-flight background load, or loads now
	}
	p.mu.Lock()
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

// Close frees the loaded model. It waits out any in-flight background load
// (loadMu) so it can't free a ctx a loader is about to publish. The provider is
// reusable afterwards (the next NewSession reloads lazily).
func (p *Parakeet) Close() {
	p.loadMu.Lock()
	defer p.loadMu.Unlock()
	p.mu.Lock()
	ctx := p.ctx
	p.ctx, p.loadedID, p.loadErr = nil, "", nil
	p.mu.Unlock()
	if ctx != nil {
		ctx.Close()
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

	f32 := audio.PCMToF32(raw)
	audioData := audio.PCMToWAV(raw)

	start := time.Now()
	text, err := s.ctx.Transcribe(f32, s.decoder)
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
			fmt.Sprintf("inference:  %.0fms (local, CPU)", inferenceMs),
			fmt.Sprintf("rtfx:       %.1fx", audioSec/(inferenceMs/1000)),
		},
	}
	sr.captureRSS()
	return sr, nil
}
