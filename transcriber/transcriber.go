package transcriber

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"
)

type NetworkMetrics struct {
	DNS         time.Duration
	ConnWait    time.Duration
	TCP         time.Duration
	TLS         time.Duration
	ReqHeaders  time.Duration
	ReqBody     time.Duration
	TTFB        time.Duration
	Download    time.Duration
	Total       time.Duration
	ConnReused  bool
	TLSProtocol string
}

func (m *NetworkMetrics) Sum() time.Duration {
	return m.ConnWait + m.DNS + m.TCP + m.TLS + m.ReqHeaders + m.ReqBody + m.TTFB + m.Download
}

func firstNonEmpty(h http.Header, keys ...string) string {
	for _, k := range keys {
		if v := h.Get(k); v != "" {
			return v
		}
	}
	return "?"
}

type Segment struct {
	Text             string
	NoSpeechProb     float64
	AvgLogProb       float64
	CompressionRatio float64
	Temperature      float64
	Start            float64
	End              float64
}

type Result struct {
	Text         string
	Metrics      *NetworkMetrics
	RateLimit    string
	Confidence   float64
	NoSpeechProb float64
	AvgLogProb   float64
	Duration     float64
	InferenceMs  float64
	Segments     []Segment
}

type ModelInfo struct {
	ID        string
	Label     string
	Stream    bool
	Languages []Language
}

type Language struct {
	Code  string // ISO-639-1 ("" = auto-detect)
	Label string
}

type Transcriber interface {
	Name() string
	SupportedLanguages() []Language
	SetLanguage(lang string)
	GetLanguage() string
	Models() []ModelInfo
	SetModel(model string)
	GetModel() string
	NewSession(ctx context.Context, cfg SessionConfig) (Session, error)
}

// langLabels maps ISO-639-1 codes to display names.
var langLabels = map[string]string{
	"af": "Afrikaans", "am": "Amharic", "ar": "Arabic", "hy": "Armenian",
	"as": "Assamese", "az": "Azerbaijani", "be": "Belarusian", "bn": "Bengali",
	"bs": "Bosnian", "bg": "Bulgarian", "my": "Burmese", "ca": "Catalan",
	"ny": "Chichewa", "zh": "Chinese", "hr": "Croatian", "cs": "Czech",
	"da": "Danish", "nl": "Dutch", "en": "English", "et": "Estonian",
	"fi": "Finnish", "fr": "French", "gl": "Galician", "ka": "Georgian",
	"de": "German", "el": "Greek", "gu": "Gujarati", "ha": "Hausa",
	"he": "Hebrew", "hi": "Hindi", "hu": "Hungarian", "is": "Icelandic",
	"ig": "Igbo", "id": "Indonesian", "ga": "Irish", "it": "Italian",
	"ja": "Japanese", "jv": "Javanese", "kn": "Kannada", "kk": "Kazakh",
	"km": "Khmer", "ko": "Korean", "ku": "Kurdish", "ky": "Kyrgyz",
	"lo": "Lao", "lv": "Latvian", "ln": "Lingala", "lt": "Lithuanian",
	"lb": "Luxembourgish", "mk": "Macedonian", "ms": "Malay", "ml": "Malayalam",
	"mt": "Maltese", "mi": "Maori", "mr": "Marathi", "mn": "Mongolian",
	"ne": "Nepali", "no": "Norwegian", "oc": "Occitan", "or": "Odia",
	"ps": "Pashto", "fa": "Persian", "pl": "Polish", "pt": "Portuguese",
	"pa": "Punjabi", "ro": "Romanian", "ru": "Russian", "sr": "Serbian",
	"sn": "Shona", "sd": "Sindhi", "sk": "Slovak", "sl": "Slovenian",
	"so": "Somali", "es": "Spanish", "sw": "Swahili", "sv": "Swedish",
	"ta": "Tamil", "tg": "Tajik", "te": "Telugu", "th": "Thai",
	"tl": "Tagalog", "tr": "Turkish", "uk": "Ukrainian", "ur": "Urdu",
	"uz": "Uzbek", "vi": "Vietnamese", "cy": "Welsh", "wo": "Wolof",
	"xh": "Xhosa", "zu": "Zulu",
}

func langsFromCodes(codes []string) []Language {
	return append([]Language{{"", "Auto-detect"}}, plainLangsFromCodes(codes)...)
}

// plainLangsFromCodes builds a language list without the Auto-detect entry,
// for providers that cannot honor it (see nova3Langs).
func plainLangsFromCodes(codes []string) []Language {
	langs := make([]Language, 0, len(codes))
	for _, c := range codes {
		label := langLabels[c]
		if label == "" {
			label = c
		}
		langs = append(langs, Language{c, label})
	}
	return langs
}

// AllLanguages returns every known language (Auto-detect first, then the rest
// sorted alphabetically). The tray uses this as the fixed universe of language
// menu items; SetLanguages then shows only the active model's subset.
func AllLanguages() []Language {
	codes := make([]string, 0, len(langLabels))
	for c := range langLabels {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	return langsFromCodes(codes)
}

type baseTranscriber struct {
	client *TracedClient
	apiURL string
	lang   string
	model  string
	langMu sync.RWMutex
}

func (b *baseTranscriber) SetLanguage(lang string) {
	b.langMu.Lock()
	b.lang = lang
	b.langMu.Unlock()
}

func (b *baseTranscriber) GetLanguage() string {
	b.langMu.RLock()
	defer b.langMu.RUnlock()
	return b.lang
}

func (b *baseTranscriber) Models() []ModelInfo { return nil }
func (b *baseTranscriber) SetModel(m string)   { b.model = m }
func (b *baseTranscriber) GetModel() string    { return b.model }

func modelLanguages(models []ModelInfo, current string) []Language {
	for _, m := range models {
		if m.ID == current {
			return m.Languages
		}
	}
	return nil
}

// ModelStatus describes whether one model is usable now, and if not whether the
// user can make it usable. Cloud providers are binary (Ready when the key is
// present, else not Downloadable → Unavailable); the local provider adds the
// downloadable middle ground.
type ModelStatus struct {
	Ready        bool
	Downloadable bool   // missing but the user can fetch it (local only)
	Detail       string // human-readable size when downloadable & missing
}

// ProviderInfo is a uniform descriptor for every backend — cloud or local. No
// provider is special-cased: New() and the tray treat them all through these
// fields. Download is nil for providers that have nothing to fetch (cloud).
type ProviderInfo struct {
	Name         string
	Label        string
	Models       []ModelInfo
	Local        bool               // on-device engine: keyless, models on disk
	DefaultModel string             // the model a fresh instance loads (local only)
	Available    func() bool        // at least one model usable right now
	New          func() Transcriber // keyless: closes over the key / model dir
	Status       func(modelID string) ModelStatus
	Download     func(modelID string, progress func(fraction float64)) error
}

// keySource resolves a provider's API key by provider name (e.g. "groq" →
// "gsk_…"). It is injected once at startup (SetKeySource) so this package keeps
// no dependency on app-level secret storage. The default resolves nothing.
var keySource = func(string) string { return "" }

// SetKeySource installs the provider→key resolver. main() wires this to
// config.APIKey (which reads credentials.json); tests inject their own. Call it
// once, before any provider is used.
func SetKeySource(fn func(provider string) string) { keySource = fn }

// cloudProvider builds a key-gated ProviderInfo. Availability is "key present";
// every model shares that status and nothing is downloadable. The key is
// resolved by provider name through the injected keySource.
func cloudProvider(name, label string, models []ModelInfo, mk func(string) Transcriber) ProviderInfo {
	hasKey := func() bool { return keySource(name) != "" }
	return ProviderInfo{
		Name:      name,
		Label:     label,
		Models:    models,
		Available: hasKey,
		New:       func() Transcriber { return mk(keySource(name)) },
		Status:    func(string) ModelStatus { return ModelStatus{Ready: hasKey()} },
	}
}

func Providers() []ProviderInfo {
	return []ProviderInfo{
		// Local is first so it's the default on a fresh machine even when cloud
		// keys are set; cloud is opt-in via the tray (the choice persists).
		// Parakeet leads Whisper: it is the English fast path (4–9× faster) and
		// the startup default, with Whisper the multilingual option next to it.
		parakeetProvider(),
		whisperProvider(),
		qwenProvider(), // POC (qwen-asr-int): CPU-only, hand-fetched model

		cloudProvider("deepgram", "Deepgram", DeepgramModels, func(k string) Transcriber { return NewDeepgram(k) }),
		cloudProvider("openai", "OpenAI", OpenAIModels, func(k string) Transcriber { return NewOpenAI(k) }),
		cloudProvider("groq", "Groq", GroqModels, func(k string) Transcriber { return NewGroq(k) }),
		cloudProvider("mistral", "Mistral", MistralModels, func(k string) Transcriber { return NewMistral(k) }),
		cloudProvider("elevenlabs", "ElevenLabs", ElevenLabsModels, func(k string) Transcriber { return NewElevenLabs(k) }),
	}
}

// New returns the default transcriber: the local model when available (even if
// cloud keys are set), else the first cloud provider with a key.
func New() (Transcriber, error) {
	if fakeText, ok := os.LookupEnv("ZEE_FAKE_TEXT"); ok {
		var fakeErr error
		if os.Getenv("ZEE_FAKE_ERROR") == "1" {
			fakeErr = fmt.Errorf("simulated API failure")
		}
		return NewFake(fakeText, fakeErr), nil
	}

	for _, p := range Providers() {
		if p.Available() {
			return p.New(), nil
		}
	}

	return nil, fmt.Errorf("no transcriber available: run `zee -setup` to add a cloud provider API key (or install on Apple Silicon to run offline)")
}
