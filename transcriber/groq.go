package transcriber

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"time"
)

const groqAPIURL = "https://api.groq.com/openai/v1/audio/transcriptions"

type Groq struct {
	apiKey string
	client *TracedClient
}

func NewGroq(apiKey string) *Groq {
	return &Groq{
		apiKey: apiKey,
		client: NewTracedClient(),
	}
}

func (g *Groq) Name() string { return "groq" }

func (g *Groq) WarmConnection() time.Duration {
	return g.client.WarmConnection(groqAPIURL)
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

func (g *Groq) Transcribe(audioData []byte, format string) (*Result, error) {
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
	writer.Close()

	req, err := http.NewRequest("POST", groqAPIURL, &body)
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
