package transcriber

import (
	"bytes"
	"errors"
	"sync"
	"testing"

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
