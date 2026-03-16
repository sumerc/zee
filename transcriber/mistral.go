package transcriber

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strconv"
)

var voxtralLangs = langsFromCodes([]string{
	"ar", "zh", "nl", "en", "fr", "de", "hi", "it", "ja", "ko",
	"pt", "ru", "es",
})

var MistralModels = []ModelInfo{
	{ID: "voxtral-mini-latest", Label: "Voxtral Mini", Stream: false, Languages: voxtralLangs},
}

type Mistral struct {
	baseTranscriber
	apiKey string
}

func NewMistral(apiKey string) *Mistral {
	apiURL := "https://api.mistral.ai/v1/audio/transcriptions"
	return &Mistral{
		baseTranscriber: baseTranscriber{
			client: NewTracedClient(apiURL),
			apiURL: apiURL,
			model:  "voxtral-mini-latest",
		},
		apiKey: apiKey,
	}
}

func (m *Mistral) SupportedLanguages() []Language { return modelLanguages(MistralModels, m.GetModel()) }
func (m *Mistral) Name() string                   { return "mistral" }
func (m *Mistral) Models() []ModelInfo             { return MistralModels }

func (m *Mistral) NewSession(_ context.Context, cfg SessionConfig) (Session, error) {
	go m.client.Warm()
	if cfg.Stream {
		return nil, fmt.Errorf("mistral does not support streaming transcription")
	}
	return newBatchSession(cfg, m.transcribe)
}

func (m *Mistral) transcribe(audioData []byte, format, lang string) (*Result, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", "audio."+format)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(audioData); err != nil {
		return nil, err
	}

	writer.WriteField("model", m.GetModel())
	if lang != "" {
		writer.WriteField("language", lang)
	}
	writer.Close()

	req, err := http.NewRequest("POST", m.apiURL, &body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("mistral API error %d: %s", resp.StatusCode, string(resp.Body))
	}

	var mResp struct {
		Text     string  `json:"text"`
		Language string  `json:"language"`
		Duration float64 `json:"duration"`
	}
	if err := json.Unmarshal(resp.Body, &mResp); err != nil {
		return nil, fmt.Errorf("mistral response parse error: %w", err)
	}

	remaining := firstNonEmpty(resp.Header, "x-ratelimit-remaining-req-minute")
	limit := firstNonEmpty(resp.Header, "x-ratelimit-limit-req-minute")
	inferenceMs, _ := strconv.ParseFloat(firstNonEmpty(resp.Header, "x-envoy-upstream-service-time"), 64)

	return &Result{
		Text:        mResp.Text,
		Metrics:     resp.Metrics,
		RateLimit:   remaining + "/" + limit,
		Duration:    mResp.Duration,
		InferenceMs: inferenceMs,
	}, nil
}
