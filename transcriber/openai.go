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
		},
		apiKey: apiKey,
	}
}

func (o *OpenAI) Name() string { return "openai" }

func (o *OpenAI) NewSession(_ context.Context, cfg SessionConfig) (Session, error) {
	go o.client.Warm()
	if cfg.Stream {
		return nil, fmt.Errorf("openai does not support streaming transcription")
	}
	if cfg.Language != "" {
		o.SetLanguage(cfg.Language)
	}
	return newBatchSession(cfg, o.transcribe)
}

func (o *OpenAI) transcribe(audioData []byte, format string) (*Result, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", "audio."+format)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(audioData); err != nil {
		return nil, err
	}

	writer.WriteField("model", "gpt-4o-transcribe")
	writer.WriteField("response_format", "json")
	if o.lang != "" {
		writer.WriteField("language", o.lang)
	}
	writer.Close()

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
