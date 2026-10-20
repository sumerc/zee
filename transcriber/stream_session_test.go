package transcriber

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"

	"zee/audio"
)

// fakeRawStream is a scripted rawStreamSession: it records every Send, and
// after CloseSend serves one final transcript (flagged FromFinalize so Close's
// finalize wait returns immediately), then blocks until Close.
type fakeRawStream struct {
	mu        sync.Mutex
	sent      []byte
	sendDone  chan struct{}
	closed    chan struct{}
	finalOnce sync.Once
	closeOnce sync.Once
}

func newFakeRawStream() *fakeRawStream {
	return &fakeRawStream{
		sendDone: make(chan struct{}),
		closed:   make(chan struct{}),
	}
}

func (f *fakeRawStream) Send(pcm []byte) error {
	f.mu.Lock()
	f.sent = append(f.sent, pcm...)
	f.mu.Unlock()
	return nil
}

func (f *fakeRawStream) CloseSend() error {
	close(f.sendDone)
	return nil
}

func (f *fakeRawStream) Recv() (streamUpdate, error) {
	select {
	case <-f.closed:
		return streamUpdate{}, errors.New("connection closed")
	case <-f.sendDone:
	}
	var final bool
	f.finalOnce.Do(func() { final = true })
	if final {
		return streamUpdate{Transcript: "hello world", IsFinal: true, FromFinalize: true}, nil
	}
	<-f.closed
	return streamUpdate{}, errors.New("connection closed")
}

func (f *fakeRawStream) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

// testPCM builds a recognizable non-zero PCM payload spanning full stream
// chunks plus a partial tail, so retention must cover both the chunked sends
// and the tail flush.
func testPCM() []byte {
	pcm := make([]byte, streamChunkBytes*2+123)
	for i := range pcm {
		pcm[i] = byte(i)
	}
	return pcm
}

// TestStreamSessionRetainsAudio: a stream session must hand back the full
// session audio in SessionResult.AudioData (as WAV), like the batch and local
// sessions do — it's what "Save Last Recording" and the auto-save-on-error
// path persist.
func TestStreamSessionRetainsAudio(t *testing.T) {
	f := newFakeRawStream()
	ss := newStreamSession(func() (rawStreamSession, error) { return f, nil })

	pcm := testPCM()
	ss.Feed(pcm)

	res, err := ss.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if res.Text != "hello world" {
		t.Fatalf("Text = %q, want %q", res.Text, "hello world")
	}
	if res.AudioFormat != "wav" {
		t.Fatalf("AudioFormat = %q, want wav", res.AudioFormat)
	}
	got, err := audio.WAVToPCM(res.AudioData)
	if err != nil {
		t.Fatalf("AudioData is not a valid WAV: %v", err)
	}
	if !bytes.Equal(got, pcm) {
		t.Fatalf("retained PCM differs from fed PCM (got %d bytes, want %d)", len(got), len(pcm))
	}
}

// TestStreamSessionRetainsAudioOnConnectError reproduces the lost-dictation
// case: the websocket never connects (offline), Close reports the error — and
// the audio spoken meanwhile must still come back so main.go can auto-save it
// to samples/.
func TestStreamSessionRetainsAudioOnConnectError(t *testing.T) {
	dialErr := errors.New("dial tcp: network is unreachable")
	ss := newStreamSession(func() (rawStreamSession, error) { return nil, dialErr })

	// Wait for the dial to fail so Feed deterministically hits the post-error
	// path — audio fed after the failure must be retained too.
	<-ss.connected

	pcm := testPCM()
	ss.Feed(pcm)

	res, err := ss.Close()
	if !errors.Is(err, dialErr) {
		t.Fatalf("Close error = %v, want %v", err, dialErr)
	}
	got, wavErr := audio.WAVToPCM(res.AudioData)
	if wavErr != nil {
		t.Fatalf("AudioData after connect error is not a valid WAV: %v (len=%d)", wavErr, len(res.AudioData))
	}
	if !bytes.Equal(got, pcm) {
		t.Fatalf("retained PCM differs from fed PCM (got %d bytes, want %d)", len(got), len(pcm))
	}
}

// stallRawStream models a network stall: every Send blocks until Close, the
// way a websocket write does when the peer stops reading.
type stallRawStream struct {
	closed    chan struct{}
	closeOnce sync.Once
}

func newStallRawStream() *stallRawStream { return &stallRawStream{closed: make(chan struct{})} }

func (f *stallRawStream) Send([]byte) error {
	<-f.closed
	return errors.New("connection closed")
}

func (f *stallRawStream) CloseSend() error { return nil }

func (f *stallRawStream) Recv() (streamUpdate, error) {
	<-f.closed
	return streamUpdate{}, errors.New("connection closed")
}

func (f *stallRawStream) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

// within fails the test if fn does not return in d.
func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s blocked for over %s", what, d)
	}
}

// TestStreamSessionStallNeverBlocksFeed reproduces the hotkey-dead hang: the
// network stalls mid-dictation, the send queue fills, and Feed — called from
// the capture callback under the recording lock — must not block on it. Close
// must then return the error with every byte of audio kept for saving.
func TestStreamSessionStallNeverBlocksFeed(t *testing.T) {
	ss := newStreamSession(func() (rawStreamSession, error) { return newStallRawStream(), nil })
	<-ss.connected

	chunk := make([]byte, streamChunkBytes)
	var fed []byte
	within(t, 2*time.Second, "Feed", func() {
		for range streamQueueChunks + 10 {
			ss.Feed(chunk)
			fed = append(fed, chunk...)
		}
	})

	var res SessionResult
	var err error
	within(t, 2*time.Second, "Close", func() { res, err = ss.Close() })
	if !errors.Is(err, errStreamStalled) {
		t.Fatalf("Close error = %v, want %v", err, errStreamStalled)
	}
	got, wavErr := audio.WAVToPCM(res.AudioData)
	if wavErr != nil {
		t.Fatalf("AudioData after a stall is not a valid WAV: %v", wavErr)
	}
	if !bytes.Equal(got, fed) {
		t.Fatalf("retained PCM differs from fed PCM (got %d bytes, want %d)", len(got), len(fed))
	}
}

// TestStreamSessionCloseBoundsStalledSend: a stall short of a full queue
// leaves the sender stuck in one write when recording ends. Close must give up
// after streamDrainMax instead of waiting on the network forever.
func TestStreamSessionCloseBoundsStalledSend(t *testing.T) {
	defer func(d time.Duration) { streamDrainMax = d }(streamDrainMax)
	streamDrainMax = 100 * time.Millisecond

	ss := newStreamSession(func() (rawStreamSession, error) { return newStallRawStream(), nil })
	ss.Feed(testPCM())

	var err error
	within(t, 2*time.Second, "Close", func() { _, err = ss.Close() })
	if err == nil {
		t.Fatal("Close on a stalled stream returned no error")
	}
}
