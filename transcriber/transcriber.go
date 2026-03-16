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
	DNS        time.Duration
	ConnWait   time.Duration
	TCP        time.Duration
	TLS        time.Duration
	ReqHeaders time.Duration
	ReqBody    time.Duration
	TTFB       time.Duration
	Download   time.Duration
	Total      time.Duration
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
	"af": "Afrikaans", "ar": "Arabic", "hy": "Armenian", "az": "Azerbaijani",
	"be": "Belarusian", "bs": "Bosnian", "bg": "Bulgarian", "ca": "Catalan",
	"zh": "Chinese", "hr": "Croatian", "cs": "Czech", "da": "Danish",
	"nl": "Dutch", "en": "English", "et": "Estonian", "fi": "Finnish",
	"fr": "French", "gl": "Galician", "de": "German", "el": "Greek",
	"he": "Hebrew", "hi": "Hindi", "hu": "Hungarian", "is": "Icelandic",
	"id": "Indonesian", "it": "Italian", "ja": "Japanese", "kn": "Kannada",
	"kk": "Kazakh", "ko": "Korean", "lv": "Latvian", "lt": "Lithuanian",
	"mk": "Macedonian", "ms": "Malay", "mr": "Marathi", "mi": "Maori",
	"ne": "Nepali", "no": "Norwegian", "fa": "Persian", "pl": "Polish",
	"pt": "Portuguese", "ro": "Romanian", "ru": "Russian", "sr": "Serbian",
	"sk": "Slovak", "sl": "Slovenian", "es": "Spanish", "sw": "Swahili",
	"sv": "Swedish", "tl": "Tagalog", "ta": "Tamil", "th": "Thai",
	"tr": "Turkish", "uk": "Ukrainian", "ur": "Urdu", "vi": "Vietnamese",
	"cy": "Welsh",
}

func langsFromCodes(codes []string) []Language {
	langs := make([]Language, 0, len(codes)+1)
	langs = append(langs, Language{"", "Auto-detect"})
	for _, c := range codes {
		label := langLabels[c]
		if label == "" {
			label = c
		}
		langs = append(langs, Language{c, label})
	}
	return langs
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

// AllLanguages returns every known language, sorted alphabetically.
func AllLanguages() []Language {
	codes := make([]string, 0, len(langLabels))
	for c := range langLabels {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	return langsFromCodes(codes)
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

func New() (Transcriber, error) {
	if fakeText, ok := os.LookupEnv("ZEE_FAKE_TEXT"); ok {
		var fakeErr error
		if os.Getenv("ZEE_FAKE_ERROR") == "1" {
			fakeErr = fmt.Errorf("simulated API failure")
		}
		return NewFake(fakeText, fakeErr), nil
	}

	dgKey := os.Getenv("DEEPGRAM_API_KEY")
	openaiKey := os.Getenv("OPENAI_API_KEY")
	groqKey := os.Getenv("GROQ_API_KEY")
	mistralKey := os.Getenv("MISTRAL_API_KEY")

	if dgKey != "" {
		return NewDeepgram(dgKey), nil
	}
	if openaiKey != "" {
		return NewOpenAI(openaiKey), nil
	}
	if groqKey != "" {
		return NewGroq(groqKey), nil
	}
	if mistralKey != "" {
		return NewMistral(mistralKey), nil
	}

	return nil, fmt.Errorf("set DEEPGRAM_API_KEY, OPENAI_API_KEY, GROQ_API_KEY, or MISTRAL_API_KEY environment variable")
}
