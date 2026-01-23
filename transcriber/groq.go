package transcriber

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptrace"
	"time"
)

const groqAPIURL = "https://api.groq.com/openai/v1/audio/transcriptions"

type Groq struct {
	apiKey string
	client *http.Client
}

func NewGroq(apiKey string) *Groq {
	return &Groq{
		apiKey: apiKey,
		client: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        1,
				MaxIdleConnsPerHost: 1,
				IdleConnTimeout:     90 * time.Second,
				ForceAttemptHTTP2:   true,
			},
		},
	}
}

func (g *Groq) Name() string { return "groq" }

func (g *Groq) WarmConnection() {
	req, _ := http.NewRequest("HEAD", groqAPIURL, nil)
	resp, err := g.client.Do(req)
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
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
	if _, err := io.Copy(part, bytes.NewReader(audioData)); err != nil {
		return nil, err
	}

	writer.WriteField("model", "whisper-large-v3-turbo")
	writer.WriteField("language", "en")
	writer.WriteField("response_format", "verbose_json")
	writer.Close()

	req, err := http.NewRequest("POST", groqAPIURL, &body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+g.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	metrics := &NetworkMetrics{}
	var dnsStart, connectStart, tlsStart, reqStart time.Time
	var wroteRequest time.Time

	trace := &httptrace.ClientTrace{
		DNSStart:             func(_ httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:              func(_ httptrace.DNSDoneInfo) { metrics.DNS = time.Since(dnsStart) },
		ConnectStart:         func(_, _ string) { connectStart = time.Now() },
		ConnectDone:          func(_, _ string, _ error) { metrics.Connect = time.Since(connectStart) },
		TLSHandshakeStart:    func() { tlsStart = time.Now() },
		TLSHandshakeDone:     func(_ tls.ConnectionState, _ error) { metrics.TLS = time.Since(tlsStart) },
		GotConn:              func(info httptrace.GotConnInfo) { metrics.ConnReused = info.Reused },
		WroteRequest:         func(_ httptrace.WroteRequestInfo) { wroteRequest = time.Now() },
		GotFirstResponseByte: func() { metrics.Inference = time.Since(wroteRequest) },
	}

	reqStart = time.Now()
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	metrics.Upload = wroteRequest.Sub(reqStart) - metrics.DNS - metrics.Connect - metrics.TLS
	if metrics.Upload < 0 {
		metrics.Upload = 0
	}

	respBody, err := io.ReadAll(resp.Body)
	metrics.Total = time.Since(reqStart)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("groq API error %d: %s", resp.StatusCode, string(respBody))
	}

	var gResp groqResponse
	if err := json.Unmarshal(respBody, &gResp); err != nil {
		return nil, fmt.Errorf("groq response parse error: %w", err)
	}

	// Aggregate segment-level scores
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
		Metrics:      metrics,
		RateLimit:    remaining + "/" + limit,
		NoSpeechProb: noSpeechProb,
		AvgLogProb:   avgLogProb,
		Duration:     gResp.Duration,
		Segments:     segments,
	}, nil
}
