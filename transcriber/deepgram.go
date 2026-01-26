package transcriber

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

func firstNonEmpty(h http.Header, keys ...string) string {
	for _, k := range keys {
		if v := h.Get(k); v != "" {
			return v
		}
	}
	return "?"
}

type Deepgram struct {
	baseTranscriber
	apiKey string
}

func NewDeepgram(apiKey string) *Deepgram {
	return &Deepgram{
		baseTranscriber: baseTranscriber{
			client: NewTracedClient(),
			apiURL: "https://api.deepgram.com/v1/listen?model=nova-3",
		},
		apiKey: apiKey,
	}
}

func (d *Deepgram) Name() string { return "deepgram" }

type deepgramResponse struct {
	Metadata struct {
		Duration float64 `json:"duration"`
		Channels int     `json:"channels"`
	} `json:"metadata"`
	Results struct {
		Channels []struct {
			Alternatives []struct {
				Transcript string  `json:"transcript"`
				Confidence float64 `json:"confidence"`
			} `json:"alternatives"`
		} `json:"channels"`
	} `json:"results"`
}

func (d *Deepgram) Transcribe(audioData []byte, format string) (*Result, error) {
	contentType := "audio/flac"
	if format == "mp3" {
		contentType = "audio/mpeg"
	}

	req, err := http.NewRequest("POST", d.apiURL, bytes.NewReader(audioData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Token "+d.apiKey)
	req.Header.Set("Content-Type", contentType)

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("deepgram API error %d: %s", resp.StatusCode, string(resp.Body))
	}

	var dgResp deepgramResponse
	if err := json.Unmarshal(resp.Body, &dgResp); err != nil {
		return nil, fmt.Errorf("deepgram response parse error: %w", err)
	}

	var text string
	var confidence float64
	if len(dgResp.Results.Channels) > 0 && len(dgResp.Results.Channels[0].Alternatives) > 0 {
		alt := dgResp.Results.Channels[0].Alternatives[0]
		text = alt.Transcript
		confidence = alt.Confidence
	}

	remaining := firstNonEmpty(resp.Header,
		"x-dg-ratelimit-remaining", "x-ratelimit-remaining", "ratelimit-remaining")
	limit := firstNonEmpty(resp.Header,
		"x-dg-ratelimit-limit", "x-ratelimit-limit", "ratelimit-limit")

	return &Result{
		Text:       text,
		Metrics:    resp.Metrics,
		RateLimit:  remaining + "/" + limit,
		Confidence: confidence,
		Duration:   dgResp.Metadata.Duration,
	}, nil
}
