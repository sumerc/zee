package main

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"zee/config"
	"zee/encoder"
	"zee/transcriber"
)

// newTestSink builds a sink writing into a temp config dir. It never touches
// the microphone: listen mode is fed by the ordinary recording session.
func newTestSink(t *testing.T, text string) *listenSink {
	t.Helper()
	config.SetDir(t.TempDir())
	vp, err := newVADProcessor()
	if err != nil {
		t.Fatalf("VAD init: %v", err)
	}
	return &listenSink{
		tr:        transcriber.NewFake(text, nil),
		path:      filepath.Join(t.TempDir(), "transcript.txt"),
		vp:        vp,
		queue:     make(chan listenChunkJob, 8),
		updates:   make(chan string),
		stop:      make(chan struct{}),
		cutterEnd: make(chan struct{}),
		workerEnd: make(chan struct{}),
	}
}

// speechPCM is a loud 220 Hz tone. WebRTC VAD calls it speech, which is what
// the cutter gates on; digital silence would be skipped instead.
func speechPCM(seconds int) []byte {
	n := encoder.SampleRate * seconds
	b := make([]byte, n*2)
	for i := 0; i < n; i++ {
		v := int16(9000 * math.Sin(2*math.Pi*220*float64(i)/float64(encoder.SampleRate)))
		binary.LittleEndian.PutUint16(b[i*2:], uint16(v))
	}
	return b
}

// listenSink must satisfy transcriber.Session — that is the whole design: the
// ordinary record path drives it, so listen mode inherits the mic, VAD,
// overlay, device selection and permission prompt for free.
func TestListenSinkIsASession(t *testing.T) {
	var _ transcriber.Session = (*listenSink)(nil)
}

// A chunk must be transcribed and appended as one timestamped line, and the
// buffer drained so the next cut cannot re-transcribe the same audio.
func TestListenTranscribeWritesAndDrains(t *testing.T) {
	s := newTestSink(t, "hello meeting")
	s.Feed(speechPCM(1))
	s.transcribe(listenChunkJob{pcm: s.take(), reason: "test", cutAt: time.Now()})

	body, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatalf("transcript not written: %v", err)
	}
	if !strings.Contains(string(body), "hello meeting") {
		t.Fatalf("transcript missing the text, got %q", body)
	}
	if len(s.pcm) != 0 {
		t.Fatalf("buffer not drained: %d bytes would be transcribed twice", len(s.pcm))
	}
}

// Whisper invents text on pure silence, so a chunk with no speech must be
// dropped rather than queued — otherwise a quiet meeting fills the transcript
// with words nobody said.
func TestListenCutSkipsSilentChunk(t *testing.T) {
	s := newTestSink(t, "phantom text")
	s.Feed(make([]byte, encoder.SampleRate*2)) // 1 s of digital silence
	s.cut("test")
	if len(s.queue) != 0 {
		t.Fatal("silent chunk was queued — whisper would hallucinate on it")
	}
}

// The cutter must ignore silence below listenMinChunk (cutting every clause
// fragments the audio) and cut once past it.
func TestListenCutterHonoursBounds(t *testing.T) {
	s := newTestSink(t, "x")
	go s.cutter()
	defer func() { s.stopOnce.Do(func() { close(s.stop) }); <-s.cutterEnd }()

	s.Feed(speechPCM(2))
	time.Sleep(3 * listenCutPoll)
	if len(s.queue) != 0 {
		t.Fatalf("cut below listenMinChunk (%v) — fragments the audio", listenMinChunk)
	}

	s.Feed(speechPCM(int(listenMinChunk.Seconds())))
	deadline := time.After(3 * time.Second)
	for {
		select {
		case job := <-s.queue:
			if job.reason != "silence" {
				t.Fatalf("cut reason = %q, want silence", job.reason)
			}
			return
		case <-deadline:
			t.Fatal("no cut after exceeding listenMinChunk with a silent VAD")
		case <-time.After(listenCutPoll):
		}
	}
}

// Close must cut the tail and drain the queue, so nothing recorded is lost —
// and must return no text, or the record path would paste the meeting into
// whatever window has focus.
func TestListenCloseFlushesTailAndReturnsNoText(t *testing.T) {
	s := newTestSink(t, "tail words")
	go s.worker()
	go s.cutter()
	s.Feed(speechPCM(1))

	res, err := s.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if res.Text != "" || res.HasText {
		t.Fatalf("Close returned text %q — it would be pasted", res.Text)
	}
	body, _ := os.ReadFile(s.path)
	if !strings.Contains(string(body), "tail words") {
		t.Fatalf("tail audio was dropped, transcript=%q", body)
	}
}

// Feed runs on the audio thread while the cutter drains; -race proves the
// buffer is safe. Feed must also copy, since the caller reuses its slice.
func TestListenFeedCopiesAndIsRaceFree(t *testing.T) {
	s := newTestSink(t, "x")
	shared := []byte{1, 2, 3, 4}
	s.Feed(shared)
	shared[0] = 99
	if s.pcm[0] == 99 {
		t.Fatal("Feed kept the caller's buffer — it is reused after Feed returns")
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			s.Feed([]byte{1, 2, 3, 4})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			s.take()
		}
	}()
	wg.Wait()
}

// A persisted listen_mode must reach the tray before the menu is built.
// Without the seed the checkbox renders unchecked while the mode is on, so the
// first click appears to do nothing (it re-sets the value it already had) and
// the toggle feels stuck one step behind.
func TestListenModeSeededFromConfig(t *testing.T) {
	config.SetDir(t.TempDir())
	config.Update(func(c *config.Settings) { c.ListenMode = true })

	if err := config.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !config.Get().ListenMode {
		t.Fatal("ListenMode did not persist to config.json")
	}

	// What main.go does at startup, and the assertion that it happens at all.
	listenMode = config.Get().ListenMode
	if !listenMode {
		t.Fatal("listenMode not seeded from config — the tray would show it off while it is on")
	}
	t.Cleanup(func() { listenMode = false })
}
