package transcriber

import (
	"fmt"
	"sort"

	"zee/internal/qwen"
	"zee/localmodel"
)

// Qwen is the on-device Qwen3-ASR provider (POC, qwen-asr-int branch). Like
// Whisper it is multilingual and takes a language per call, so the tray's
// language menu is live for it — but it runs on CPU only, so treat its latency
// as a different class rather than a slower constant.
//
// The engine matches languages by NAME ("Turkish"), not ISO-639-1 code, and
// supports 30 of them. qwenLanguages below is the intersection of that list
// with zee's label table, so the menu can only ever offer a code that maps.

// qwenNameByCode maps zee's ISO-639-1 codes to the language names the engine
// accepts. Built once from the engine's own list so the two can never drift:
// if upstream adds a language, it appears here as soon as the names match.
var qwenNameByCode = func() map[string]string {
	byName := make(map[string]string, len(langLabels))
	for code, label := range langLabels {
		byName[label] = code
	}
	out := make(map[string]string)
	for _, name := range qwen.Languages() {
		if code, ok := byName[name]; ok {
			out[code] = name
		}
	}
	return out
}()

// qwenEngine adapts a loaded Qwen model to localEngine.
type qwenEngine struct{ ctx *qwen.Ctx }

func (e qwenEngine) Transcribe(pcm []float32, lang, hints string) (string, error) {
	name := "" // "" = auto-detect
	if lang != "" {
		var ok bool
		if name, ok = qwenNameByCode[lang]; !ok {
			return "", fmt.Errorf("qwen: language %q not supported", lang)
		}
	}
	return e.ctx.Transcribe(pcm, name, hints)
}

func (e qwenEngine) Close() { e.ctx.Close() }

func openQwen(m localmodel.Model) (localEngine, error) {
	ctx, err := qwen.New(localmodel.Path(m))
	if err != nil {
		return nil, err
	}
	return qwenEngine{ctx: ctx}, nil
}

// qwenLanguages offers auto-detect plus exactly the languages the engine
// accepts. Sorted because the map iteration that produces the codes is not,
// and the tray renders this list in order.
func qwenLanguages(localmodel.Model) []Language {
	codes := make([]string, 0, len(qwenNameByCode))
	for code := range qwenNameByCode {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return langsFromCodes(codes) // prepends Auto-detect
}

func qwenProvider() ProviderInfo {
	return localProviderInfo(
		localmodel.EngineQwen, "Local (Qwen3-ASR)",
		localmodel.IDQwen06B, "", // "" = auto-detect
		qwen.Available(), true, // hints: fed in as the system prompt
		openQwen, qwenLanguages,
	)
}
