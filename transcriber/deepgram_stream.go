package transcriber

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"nhooyr.io/websocket"
)

type streamSessionConfig struct {
	SampleRate int
	Channels   int
	Language   string
	Model      string
	Hints      string
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
	} else {
		// The tray never offers Auto-detect for Deepgram (see nova3Langs), but
		// "" can still arrive from stale configs or headless flags. Omitting
		// the param silently means English-only — non-English speech comes
		// back as empty finals ("no speech") — so send nova-3's multilingual
		// mode as the least-wrong interpretation.
		q.Set("language", "multi")
	}
	if cfg.Hints != "" {
		for _, term := range strings.Split(cfg.Hints, ",") {
			q.Add("keyterm", strings.TrimSpace(term))
		}
	}
	endpoint.RawQuery = q.Encode()

	headers := http.Header{}
	headers.Set("Authorization", "Token "+d.apiKey)

	streamCtx, cancel := context.WithCancel(ctx)
	// Bound the handshake so a dead network fails in seconds — but WITHOUT a
	// context deadline: the dial ctx stays attached to the upgraded connection,
	// so cancelling it (deferred cancel or a fired timer) kills the live
	// stream mid-session. Instead dial on streamCtx and time out around it;
	// the timeout path cancels streamCtx, aborting the in-flight dial.
	type dialResult struct {
		conn *websocket.Conn
		err  error
	}
	dialCh := make(chan dialResult, 1)
	go func() {
		c, _, err := websocket.Dial(streamCtx, endpoint.String(), &websocket.DialOptions{HTTPHeader: headers})
		dialCh <- dialResult{c, err}
	}()
	var conn *websocket.Conn
	select {
	case r := <-dialCh:
		if r.err != nil {
			cancel()
			return nil, r.err
		}
		conn = r.conn
	case <-time.After(15 * time.Second):
		cancel()
		// cancel() aborts an in-flight dial, but one that *just* succeeded is
		// already upgraded and immune — close it, or the socket leaks for the
		// life of the process.
		go func() {
			if r := <-dialCh; r.conn != nil {
				r.conn.Close(websocket.StatusGoingAway, "dial timed out")
			}
		}()
		return nil, fmt.Errorf("deepgram: connect timed out after 15s")
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
