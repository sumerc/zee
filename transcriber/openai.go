package transcriber

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
)

type OpenAI struct {
	baseTranscriber
	apiKey string
}

func NewOpenAI(apiKey string) *OpenAI {
	apiURL := "https://api.openai.com/v1/audio/transcriptions"
	return &OpenAI{
		baseTranscriber: baseTranscriber{
			client: NewTracedClient(apiURL),
			apiURL: apiURL,
			model:  "gpt-4o-transcribe",
		},
		apiKey: apiKey,
	}
}

func (o *OpenAI) SupportedLanguages() []Language { return modelLanguages(OpenAIModels, o.GetModel()) }
func (o *OpenAI) Name() string                   { return "openai" }

var gpt4oTranscribeLangs = langsFromCodes([]string{
	"af", "ar", "hy", "az", "be", "bs", "bg", "ca", "zh", "hr",
	"cs", "da", "nl", "en", "et", "fi", "fr", "gl", "de", "el",
	"he", "hi", "hu", "is", "id", "it", "ja", "kn", "kk", "ko",
	"lv", "lt", "mk", "ms", "mr", "mi", "ne", "no", "fa", "pl",
	"pt", "ro", "ru", "sr", "sk", "sl", "es", "sw", "sv", "tl",
	"ta", "th", "tr", "uk", "ur", "vi", "cy",
})

var OpenAIModels = []ModelInfo{
	{ID: "gpt-4o-transcribe", Label: "GPT-4o Transcribe", Stream: false, Languages: gpt4oTranscribeLangs},
}

func (o *OpenAI) Models() []ModelInfo { return OpenAIModels }

func (o *OpenAI) NewSession(_ context.Context, cfg SessionConfig) (Session, error) {
	go o.client.Warm()
	if cfg.Stream {
		return nil, fmt.Errorf("openai does not support streaming transcription")
	}
	return newBatchSession(cfg, o.Transcribe)
}

func (o *OpenAI) Transcribe(audioData []byte, format, lang, hint string) (*Result, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", "audio."+format)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(audioData); err != nil {
		return nil, err
	}

	writer.WriteField("model", o.GetModel())
	writer.WriteField("response_format", "json")
	if lang != "" {
		writer.WriteField("language", lang)
	}
	if hint != "" {
		writer.WriteField("prompt", hint)
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", o.apiURL, &body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("openai API error %d: %s", resp.StatusCode, string(resp.Body))
	}

	var oResp struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(resp.Body, &oResp); err != nil {
		return nil, fmt.Errorf("openai response parse error: %w", err)
	}

	remaining := firstNonEmpty(resp.Header, "x-ratelimit-remaining-requests")
	limit := firstNonEmpty(resp.Header, "x-ratelimit-limit-requests")

	return &Result{
		Text:      oResp.Text,
		Metrics:   resp.Metrics,
		RateLimit: remaining + "/" + limit,
	}, nil
}
