package transcriber

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"nhooyr.io/websocket"
)

type streamSessionConfig struct {
	SampleRate int
	Channels   int
	Language   string
	Model      string
}

type deepgramStreamResponse struct {
	Type         string `json:"type"`
	IsFinal      bool   `json:"is_final"`
	SpeechFinal  bool   `json:"speech_final"`
	FromFinalize bool   `json:"from_finalize"`
	Channel      struct {
		Alternatives []struct {
			Transcript string `json:"transcript"`
		} `json:"alternatives"`
	} `json:"channel"`
}

type deepgramStreamSession struct {
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
}

func (d *Deepgram) startStream(ctx context.Context, cfg streamSessionConfig) (rawStreamSession, error) {
	endpoint, err := url.Parse("wss://api.deepgram.com/v1/listen")
	if err != nil {
		return nil, err
	}

	q := endpoint.Query()
	model := cfg.Model
	if model == "" {
		model = "nova-3"
	}
	q.Set("model", model)
	q.Set("encoding", "linear16")
	if cfg.SampleRate > 0 {
		q.Set("sample_rate", fmt.Sprintf("%d", cfg.SampleRate))
	}
	if cfg.Channels > 0 {
		q.Set("channels", fmt.Sprintf("%d", cfg.Channels))
	}
	if cfg.Language != "" {
		q.Set("language", cfg.Language)
	}
	endpoint.RawQuery = q.Encode()

	headers := http.Header{}
	headers.Set("Authorization", "Token "+d.apiKey)

	streamCtx, cancel := context.WithCancel(ctx)
	conn, _, err := websocket.Dial(streamCtx, endpoint.String(), &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		cancel()
		return nil, err
	}

	return &deepgramStreamSession{conn: conn, ctx: streamCtx, cancel: cancel}, nil
}

func (s *deepgramStreamSession) Send(pcm []byte) error {
	return s.conn.Write(s.ctx, websocket.MessageBinary, pcm)
}

func (s *deepgramStreamSession) CloseSend() error {
	msg := []byte(`{"type":"Finalize"}`)
	return s.conn.Write(s.ctx, websocket.MessageText, msg)
}

func (s *deepgramStreamSession) Recv() (streamUpdate, error) {
	_, data, err := s.conn.Read(s.ctx)
	if err != nil {
		return streamUpdate{}, err
	}

	var resp deepgramStreamResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return streamUpdate{}, err
	}

	transcript := ""
	if len(resp.Channel.Alternatives) > 0 {
		transcript = resp.Channel.Alternatives[0].Transcript
	}

	return streamUpdate{
		Transcript:   strings.TrimSpace(transcript),
		IsFinal:      resp.IsFinal,
		SpeechFinal:  resp.SpeechFinal,
		FromFinalize: resp.FromFinalize,
	}, nil
}

func (s *deepgramStreamSession) Close() error {
	s.cancel()
	return s.conn.Close(websocket.StatusNormalClosure, "")
}
