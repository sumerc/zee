package transcriber

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptrace"
	"time"
)

type TracedClient struct {
	client *http.Client
}

func NewTracedClient() *TracedClient {
	return &TracedClient{
		client: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        4,
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:     90 * time.Second,
				ForceAttemptHTTP2:   true,
			},
		},
	}
}

type TracedResponse struct {
	Body       []byte
	StatusCode int
	Header     http.Header
	Metrics    *NetworkMetrics
}

func (c *TracedClient) Do(req *http.Request) (*TracedResponse, error) {
	metrics := &NetworkMetrics{}
	var getConnStart, dnsStart, tcpStart, tlsStart time.Time
	var gotConn, wroteHeaders, wroteRequest, firstByte time.Time

	trace := &httptrace.ClientTrace{
		GetConn: func(_ string) { getConnStart = time.Now() },
		GotConn: func(info httptrace.GotConnInfo) {
			gotConn = time.Now()
			metrics.ConnWait = gotConn.Sub(getConnStart)
			metrics.ConnReused = info.Reused
		},
		DNSStart:      func(_ httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:       func(_ httptrace.DNSDoneInfo) { metrics.DNS = time.Since(dnsStart) },
		ConnectStart:  func(_, _ string) { tcpStart = time.Now() },
		ConnectDone:   func(_, _ string, _ error) { metrics.TCP = time.Since(tcpStart) },
		TLSHandshakeStart: func() { tlsStart = time.Now() },
		TLSHandshakeDone:  func(_ tls.ConnectionState, _ error) { metrics.TLS = time.Since(tlsStart) },
		WroteHeaders: func() {
			wroteHeaders = time.Now()
			metrics.ReqHeaders = wroteHeaders.Sub(gotConn)
		},
		WroteRequest: func(_ httptrace.WroteRequestInfo) {
			wroteRequest = time.Now()
			metrics.ReqBody = wroteRequest.Sub(wroteHeaders)
		},
		GotFirstResponseByte: func() {
			firstByte = time.Now()
			metrics.TTFB = firstByte.Sub(wroteRequest)
		},
	}

	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	reqStart := time.Now()

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	metrics.Download = time.Since(firstByte)
	metrics.Total = time.Since(reqStart)

	return &TracedResponse{
		Body:       body,
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Metrics:    metrics,
	}, nil
}

func (c *TracedClient) WarmConnection(url string) time.Duration {
	var tlsStart time.Time
	var tlsDuration time.Duration

	trace := &httptrace.ClientTrace{
		TLSHandshakeStart: func() { tlsStart = time.Now() },
		TLSHandshakeDone:  func(_ tls.ConnectionState, _ error) { tlsDuration = time.Since(tlsStart) },
	}

	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return 0
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	resp, err := c.client.Do(req)
	if err != nil {
		return 0
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return tlsDuration
}
