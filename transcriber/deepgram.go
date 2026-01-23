package transcriber

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"time"
)

const deepgramAPIURL = "https://api.deepgram.com/v1/listen?model=nova-3&language=en"

func firstNonEmpty(h http.Header, keys ...string) string {
	for _, k := range keys {
		if v := h.Get(k); v != "" {
			return v
		}
	}
	return "?"
}

type Deepgram struct {
	apiKey string
	client *http.Client
}

func NewDeepgram(apiKey string) *Deepgram {
	return &Deepgram{
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

func (d *Deepgram) Name() string { return "deepgram" }

func (d *Deepgram) WarmConnection() {
	req, _ := http.NewRequest("HEAD", "https://api.deepgram.com", nil)
	resp, err := d.client.Do(req)
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

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

	req, err := http.NewRequest("POST", deepgramAPIURL, bytes.NewReader(audioData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Token "+d.apiKey)
	req.Header.Set("Content-Type", contentType)

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

	resp, err := d.client.Do(req)
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
		return nil, fmt.Errorf("deepgram API error %d: %s", resp.StatusCode, string(respBody))
	}

	var dgResp deepgramResponse
	if err := json.Unmarshal(respBody, &dgResp); err != nil {
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
		Metrics:    metrics,
		RateLimit:  remaining + "/" + limit,
		Confidence: confidence,
		Duration:   dgResp.Metadata.Duration,
	}, nil
}
