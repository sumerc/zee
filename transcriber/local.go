package transcriber

import (
	"context"
	"fmt"
	"sync"

	"zee/audio"
	"zee/localmodel"
)

// localEngine is the entire engine-specific surface of an on-device provider:
// one loaded model that turns PCM into text. Everything else — the desired vs
// loaded model bookkeeping, the background load, the swap, the session — is
// identical between engines and lives on localProvider below.
//
// lang is an ISO-639-1 code, "" meaning auto-detect. Parakeet has no language
// parameter at all and ignores it (its models are single-language by build);
// whisper honours it. hints is vocabulary biasing — the same string the cloud
// providers send as `prompt`; whisper feeds it in as the initial prompt,
// parakeet has nowhere to put it. "" for either means "unset".
type localEngine interface {
	Transcribe(pcm []float32, lang, hints string) (string, error)
	Close()
}

// localProvider is the offline, on-device provider shared by every local
// engine. It wraps one loaded model (decision #2: a single shared ctx;
// push-to-talk is serial) and swaps the file to change models (decision #3: the
// default is warmed in the background at startup, and switches reload in the
// background too, so the UI never blocks).
type localProvider struct {
	mu       sync.Mutex // guards the fields below; held only briefly, never across a model load
	loadMu   sync.Mutex // serializes load() so concurrent triggers can't double-load
	modelID  string     // desired model
	loadedID string     // model currently loaded ("" while unloaded or loading)
	engine   localEngine
	loadErr  error
	lang     string

	name      string                                      // provider name, e.g. "parakeet"
	defaultID string                                      // this engine's model, used when modelID belongs to another
	hints     bool                                        // engine can bias decoding toward a vocabulary
	open      func(localmodel.Model) (localEngine, error) // load a model with this engine
	langsFor  func(localmodel.Model) []Language
}

// newLocalProvider builds a provider around one engine and warms its default
// model in the background so construction never blocks startup. The load holds
// only loadMu (not mu) while it reads the model, so metadata accessors stay
// responsive; the first NewSession waits for it via load()'s idempotent fast
// path. A missing or failed model surfaces at NewSession as an error.
func newLocalProvider(name, defaultID, defaultLang string, hints bool,
	open func(localmodel.Model) (localEngine, error),
	langsFor func(localmodel.Model) []Language) *localProvider {

	p := &localProvider{
		modelID:   defaultID,
		defaultID: defaultID,
		lang:      defaultLang,
		name:      name,
		hints:     hints,
		open:      open,
		langsFor:  langsFor,
	}
	go p.load()
	return p
}

// localModels lists the registry entries belonging to one engine.
func localModels(engine string) []localmodel.Model {
	var out []localmodel.Model
	for _, m := range localmodel.All() {
		if m.Engine == engine {
			out = append(out, m)
		}
	}
	return out
}

// localProviderInfo is the registry entry shared by every local engine:
// availability is "default model on disk", status is per-file presence, and
// missing models are downloadable. compiledIn is the engine's cgo gate.
func localProviderInfo(name, label, defaultID, defaultLang string, compiledIn, hints bool,
	open func(localmodel.Model) (localEngine, error),
	langsFor func(localmodel.Model) []Language) ProviderInfo {

	models := localModels(name)
	return ProviderInfo{
		Name:         name,
		Label:        label,
		Models:       modelInfos(models, langsFor),
		Local:        true,
		DefaultModel: defaultID,
		Available: func() bool {
			m, ok := localmodel.ByID(defaultID)
			return compiledIn && ok && localmodel.Present(m)
		},
		New: func() Transcriber {
			return newLocalProvider(name, defaultID, defaultLang, hints, open, langsFor)
		},
		Status: func(id string) ModelStatus {
			m, ok := localmodel.ByID(id)
			// An ID belonging to the other engine is rejected, not reported ready:
			// feeding whisper weights to the parakeet loader (a hand-edited config
			// pair) has hung for 8+ minutes on CI rather than failing cleanly.
			if !ok || !compiledIn || m.Engine != name {
				return ModelStatus{} // unknown, wrong engine, or not compiled in → Unavailable
			}
			if localmodel.Present(m) {
				return ModelStatus{Ready: true}
			}
			return ModelStatus{Downloadable: true, Detail: m.HumanSize()}
		},
		Download: func(id string, progress func(float64)) error {
			m, ok := localmodel.ByID(id)
			if !ok || m.Engine != name {
				return fmt.Errorf("unknown %s model %q", name, id)
			}
			return localmodel.Download(m, progress)
		},
	}
}

// modelInfos renders registry entries as ModelInfo (for the tray).
func modelInfos(models []localmodel.Model, langsFor func(localmodel.Model) []Language) []ModelInfo {
	out := make([]ModelInfo, 0, len(models))
	for _, m := range models {
		out = append(out, ModelInfo{ID: m.ID, Label: m.Label, Stream: false, Languages: langsFor(m)})
	}
	return out
}

// load brings the engine in line with the desired modelID, freeing the previous
// one. It's serialized by loadMu and idempotent: if the desired model is already
// loaded it returns at once, so NewSession can call it to *wait out* an in-flight
// background load without triggering a reload. The slow model read runs with mu
// released, so GetModel/SetLanguage/etc. never block on it.
func (p *localProvider) load() {
	p.loadMu.Lock()
	defer p.loadMu.Unlock()

	p.mu.Lock()
	want := p.modelID
	if p.engine != nil && p.loadedID == want {
		p.mu.Unlock()
		return
	}
	old := p.engine
	p.engine, p.loadedID, p.loadErr = nil, "", nil
	p.mu.Unlock()

	if old != nil {
		old.Close()
	}

	var (
		eng localEngine
		err error
	)
	// A model ID belonging to another engine is not an error, it is the normal
	// state right after a provider switch: the ID persisted in config.json is
	// whatever the PREVIOUS provider had selected, and it arrives here via
	// SetModel before the tray ever offers this engine's own list. Fall back to
	// this engine's default instead of failing every recording until the user
	// happens to open the model menu. Falling back is safe because the default
	// is by construction the right engine's file — what must never happen is
	// loading another engine's weights, which is a hang, not an error.
	if m, ok := localmodel.ByID(want); !ok || m.Engine != p.name {
		want = p.defaultID
		p.mu.Lock()
		p.modelID = want
		p.mu.Unlock()
	}

	if m, ok := localmodel.ByID(want); !ok || m.Engine != p.name {
		err = fmt.Errorf("unknown %s model %q", p.name, want)
	} else if !localmodel.Present(m) {
		err = fmt.Errorf("model %q not downloaded", m.Label)
	} else {
		eng, err = p.open(m) // slow; mu released
	}

	p.mu.Lock()
	p.engine, p.loadErr, p.loadedID = eng, err, want
	p.mu.Unlock()
}

// IsLocal reports whether tr is an on-device provider. Local decode has no
// streaming and no audio encoding, so the UI greys those out. Hints are a
// per-engine capability, not a local/cloud one — ask SupportsHints.
func IsLocal(tr Transcriber) bool {
	_, ok := tr.(*localProvider)
	return ok
}

// SupportsHints reports whether tr can bias decoding toward the vocabulary in
// hints.txt, so the tray greys the hints entry out for the engines that cannot
// (parakeet). Every cloud provider takes hints in some form.
func SupportsHints(tr Transcriber) bool {
	p, ok := tr.(*localProvider)
	return !ok || p.hints
}

func (p *localProvider) Name() string { return p.name }

func (p *localProvider) Models() []ModelInfo {
	return modelInfos(localModels(p.name), p.langsFor)
}

func (p *localProvider) SupportedLanguages() []Language {
	if m, ok := localmodel.ByID(p.GetModel()); ok {
		return p.langsFor(m)
	}
	return []Language{{Code: "en", Label: "English"}}
}

func (p *localProvider) SetLanguage(lang string) { p.mu.Lock(); p.lang = lang; p.mu.Unlock() }
func (p *localProvider) GetLanguage() string     { p.mu.Lock(); defer p.mu.Unlock(); return p.lang }

func (p *localProvider) GetModel() string { p.mu.Lock(); defer p.mu.Unlock(); return p.modelID }

// SetModel records the desired model and warms it in the background, so a tray
// switch returns immediately instead of blocking the UI on the model load. The
// next NewSession waits for the load (the record→inference guard keeps switches
// out of an active cycle, and recording overlaps the load, hiding its latency);
// a load failure surfaces there.
func (p *localProvider) SetModel(id string) {
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
// (-transcribe) has a single shape. Local decode accepts WAV only; hints reach
// whichever engine can use them.
func (p *localProvider) Transcribe(audioData []byte, format, lang, hints string) (*Result, error) {
	if format != "wav" {
		return nil, fmt.Errorf("local transcription supports WAV files only (got %s)", format)
	}
	pcm, err := audio.WAVToPCM(audioData)
	if err != nil {
		return nil, fmt.Errorf("cannot read WAV: %w", err)
	}
	sess, err := p.NewSession(context.Background(), SessionConfig{Language: lang, Hints: hints})
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

func (p *localProvider) NewSession(_ context.Context, cfg SessionConfig) (Session, error) {
	if cfg.Stream {
		return nil, fmt.Errorf("%s does not support streaming", p.name)
	}
	p.mu.Lock()
	ready := p.engine != nil && p.loadedID == p.modelID
	p.mu.Unlock()
	if !ready {
		p.load() // waits out an in-flight background load, or loads now
	}
	p.mu.Lock()
	eng, err := p.engine, p.loadErr
	lang := p.lang
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	// A concurrent SetModel/Close between load() returning and the read above
	// can null the engine with no error recorded. Refuse rather than hand the
	// session a nil engine (which would panic mid-dictation).
	if eng == nil {
		return nil, fmt.Errorf("%s: model %q is reloading, try again", p.name, p.GetModel())
	}
	// An explicit per-session language (the -transcribe path) wins over the
	// provider's current setting; the live hotkey path leaves it empty.
	if cfg.Language != "" {
		lang = cfg.Language
	}
	return &localSession{engine: eng, lang: lang, hints: cfg.Hints, updates: make(chan string)}, nil
}

// Close frees the loaded model. It waits out any in-flight background load
// (loadMu) so it can't free an engine a loader is about to publish. The provider
// is reusable afterwards (the next NewSession reloads lazily).
func (p *localProvider) Close() {
	p.loadMu.Lock()
	defer p.loadMu.Unlock()
	p.mu.Lock()
	eng := p.engine
	p.engine, p.loadedID, p.loadErr = nil, "", nil
	p.mu.Unlock()
	if eng != nil {
		eng.Close()
	}
}
