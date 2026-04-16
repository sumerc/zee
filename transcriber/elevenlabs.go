package transcriber

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
)

const ModelScribeV2 = "scribe_v2"

var scribeV2Langs = langsFromCodes([]string{
	"af", "am", "ar", "hy", "as", "az", "be", "bn", "bs", "bg",
	"my", "ca", "ny", "hr", "cs", "da", "nl", "en", "et", "fi",
	"fr", "gl", "ka", "de", "el", "gu", "ha", "he", "hi", "hu",
	"is", "ig", "id", "ga", "it", "ja", "jv", "kn", "kk", "km",
	"ko", "ku", "ky", "lo", "lv", "ln", "lt", "lb", "mk", "ms",
	"ml", "mt", "zh", "mi", "mr", "mn", "ne", "no", "oc", "or",
	"ps", "fa", "pl", "pt", "pa", "ro", "ru", "sr", "sn", "sd",
	"sk", "sl", "so", "es", "sw", "sv", "ta", "tg", "te", "th",
	"tr", "uk", "ur", "uz", "vi", "cy", "wo", "xh", "zu",
})

var ElevenLabsModels = []ModelInfo{
	{ID: ModelScribeV2, Label: "Scribe V2", Stream: false, Languages: scribeV2Langs},
}

type ElevenLabs struct {
	baseTranscriber
	apiKey string
}

func NewElevenLabs(apiKey string) *ElevenLabs {
	apiURL := "https://api.elevenlabs.io/v1/speech-to-text"
	return &ElevenLabs{
		baseTranscriber: baseTranscriber{
			client: NewTracedClient(apiURL),
			apiURL: apiURL,
			model:  ModelScribeV2,
		},
		apiKey: apiKey,
	}
}

func (e *ElevenLabs) SupportedLanguages() []Language {
	return modelLanguages(ElevenLabsModels, e.GetModel())
}
func (e *ElevenLabs) Name() string          { return "elevenlabs" }
func (e *ElevenLabs) Models() []ModelInfo    { return ElevenLabsModels }

func (e *ElevenLabs) NewSession(_ context.Context, cfg SessionConfig) (Session, error) {
	go e.client.Warm()
	if cfg.Stream {
		return nil, fmt.Errorf("elevenlabs does not support streaming transcription")
	}
	return newBatchSession(cfg, e.Transcribe)
}

type elevenLabsResponse struct {
	Text               string  `json:"text"`
	LanguageCode       string  `json:"language_code"`
	LanguageProbability float64 `json:"language_probability"`
	Words              []struct {
		Text   string  `json:"text"`
		Type   string  `json:"type"`
		Start  float64 `json:"start"`
		End    float64 `json:"end"`
		LogProb float64 `json:"logprob"`
	} `json:"words"`
}

func (e *ElevenLabs) Transcribe(audioData []byte, format, lang, hint string) (*Result, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", "audio."+format)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(audioData); err != nil {
		return nil, err
	}

	writer.WriteField("model_id", e.GetModel())
	if lang != "" {
		writer.WriteField("language_code", lang)
	}
	writer.WriteField("tag_audio_events", "false")
	if hint != "" {
		for _, word := range strings.Split(hint, ",") {
			writer.WriteField("keyterms[]", strings.TrimSpace(word))
		}
	}
	writer.Close()

	req, err := http.NewRequest("POST", e.apiURL, &body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("xi-api-key", e.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("elevenlabs API error %d: %s", resp.StatusCode, string(resp.Body))
	}

	var elResp elevenLabsResponse
	if err := json.Unmarshal(resp.Body, &elResp); err != nil {
		return nil, fmt.Errorf("elevenlabs response parse error: %w", err)
	}

	var avgLogProb float64
	var wordCount int
	var duration float64
	for _, w := range elResp.Words {
		if w.Type == "word" {
			avgLogProb += w.LogProb
			wordCount++
			if w.End > duration {
				duration = w.End
			}
		}
	}
	if wordCount > 0 {
		avgLogProb /= float64(wordCount)
	}

	return &Result{
		Text:       elResp.Text,
		Metrics:    resp.Metrics,
		Confidence: elResp.LanguageProbability,
		AvgLogProb: avgLogProb,
		Duration:   duration,
	}, nil
}
