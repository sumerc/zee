package transcriber

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
)

type Groq struct {
	baseTranscriber
	apiKey string
}

func NewGroq(apiKey string) *Groq {
	g := &Groq{
		baseTranscriber: baseTranscriber{
			client: NewTracedClient(),
			apiURL: "https://api.groq.com/openai/v1/audio/transcriptions",
		},
		apiKey: apiKey,
	}
	go g.warmConnection()
	return g
}

func (g *Groq) Name() string { return "groq" }

func (g *Groq) NewSession(_ context.Context, cfg SessionConfig) (Session, error) {
	if cfg.Stream {
		return nil, fmt.Errorf("groq does not support streaming transcription")
	}
	if cfg.Language != "" {
		g.SetLanguage(cfg.Language)
	}
	return newBatchSession(cfg, g.transcribe, func() { g.warmConnection() })
}

type groqResponse struct {
	Text     string  `json:"text"`
	Duration float64 `json:"duration"`
	Segments []struct {
		Text             string  `json:"text"`
		Start            float64 `json:"start"`
		End              float64 `json:"end"`
		NoSpeechProb     float64 `json:"no_speech_prob"`
		AvgLogProb       float64 `json:"avg_logprob"`
		CompressionRatio float64 `json:"compression_ratio"`
		Temperature      float64 `json:"temperature"`
	} `json:"segments"`
}

func (g *Groq) transcribe(audioData []byte, format string) (*Result, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	filename := "audio.flac"
	if format == "mp3" {
		filename = "audio.mp3"
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(audioData); err != nil {
		return nil, err
	}

	writer.WriteField("model", "whisper-large-v3-turbo")
	writer.WriteField("response_format", "verbose_json")
	if g.baseTranscriber.lang != "" {
		writer.WriteField("language", g.baseTranscriber.lang)
	}
	writer.Close()

	req, err := http.NewRequest("POST", g.apiURL, &body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+g.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("groq API error %d: %s", resp.StatusCode, string(resp.Body))
	}

	var gResp groqResponse
	if err := json.Unmarshal(resp.Body, &gResp); err != nil {
		return nil, fmt.Errorf("groq response parse error: %w", err)
	}

	var noSpeechProb, avgLogProb float64
	var segments []Segment
	if len(gResp.Segments) > 0 {
		var logProbSum float64
		for _, seg := range gResp.Segments {
			if seg.NoSpeechProb > noSpeechProb {
				noSpeechProb = seg.NoSpeechProb
			}
			logProbSum += seg.AvgLogProb
			segments = append(segments, Segment{
				Text:             seg.Text,
				NoSpeechProb:     seg.NoSpeechProb,
				AvgLogProb:       seg.AvgLogProb,
				CompressionRatio: seg.CompressionRatio,
				Temperature:      seg.Temperature,
				Start:            seg.Start,
				End:              seg.End,
			})
		}
		avgLogProb = logProbSum / float64(len(gResp.Segments))
	}

	remaining := resp.Header.Get("x-ratelimit-remaining-requests")
	limit := resp.Header.Get("x-ratelimit-limit-requests")
	if remaining == "" {
		remaining = "?"
	}
	if limit == "" {
		limit = "?"
	}

	return &Result{
		Text:         gResp.Text,
		Metrics:      resp.Metrics,
		RateLimit:    remaining + "/" + limit,
		NoSpeechProb: noSpeechProb,
		AvgLogProb:   avgLogProb,
		Duration:     gResp.Duration,
		Segments:     segments,
	}, nil
}
