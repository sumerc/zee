package main

// Listen mode (POC): meeting capture. It is a destination, not a second
// capture path — the mic, VAD, overlay, feedback sounds, device selection and
// TCC permission prompt all come from the ordinary recording flow. The single
// difference is where the text goes: appended to transcript.txt in chunks as
// the meeting runs, instead of one transcript to the clipboard at the end.
//
// It plugs in as a transcriber.Session, so handleRecording drives it exactly
// as it drives a normal session — Feed on every capture callback, Close at the
// end. A first attempt owned its own audio.CaptureDevice and bypassed all of
// that; it recorded silence for four minutes because nothing had prompted for
// microphone permission, on the wrong device, with no overlay to show it.
//
// Known POC limits: chunks decode independently (whisper runs with
// no_context), so a sentence split across a cut loses its thread even at a
// clean silence boundary — overlap-and-merge is the real fix. No speaker
// attribution.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"zee/config"
	"zee/encoder"
	"zee/log"
	"zee/transcriber"
	"zee/tray"
)

// Chunking bounds. A cut is preferred at a silence, but only inside a window:
// below listenMinChunk silence is ignored (cutting at every clause boundary
// fragments the audio, and whisper decodes a two-word fragment far worse than
// a sentence — it is trained on 30 s windows), and at listenMaxChunk the cut
// happens regardless so no chunk approaches that window.
//
// listenMinSilence is 500 ms: a clause boundary, above the 200-300 ms gaps
// inside a sentence and safely above stop consonants. At 20 ms VAD frames that
// is 25 consecutive non-speech frames, so one misclassified frame cannot cut.
// The field converges here — faster-whisper setups use 300-500 ms, Silero
// defaults to 2000 ms; nobody cuts as short as 200 ms.
const (
	listenMinSilence = 500 * time.Millisecond
	listenMinChunk   = 8 * time.Second
	listenMaxChunk   = 25 * time.Second
	listenCutPoll    = 100 * time.Millisecond
)

// listenTranscriptFile sits in the config dir, beside config.json and the
// samples — where every other artefact the app produces lives.
const listenTranscriptFile = "transcript.txt"

// listenMode is the live setting, mirrored from config so the record path can
// read it without touching the file. Guarded by configMu, like autoPaste.
var listenMode bool

type listenChunkJob struct {
	pcm    []byte
	reason string // why the cut happened: "silence", "maxlen" or "stop"
	cutAt  time.Time
}

// listenSink is the transcriber.Session that writes a meeting to disk.
type listenSink struct {
	tr    transcriber.Transcriber
	lang  string
	hints string
	path  string
	vp    *vadProcessor

	mu  sync.Mutex
	pcm []byte // raw S16LE from Feed, drained by the cutter

	// queue is deliberately generous rather than bounded: dropping a meeting is
	// worse than holding memory. If it ever filled, the cutter would block and
	// the audio would accumulate in pcm instead — the same backlog, one buffer
	// earlier. listen_chunk logs depth and lag so a real one shows up first.
	queue chan listenChunkJob

	updates   chan string
	stop      chan struct{}
	stopOnce  sync.Once
	cutterEnd chan struct{}
	workerEnd chan struct{}

	written int // chunks appended, for the closing log line
}

// newListenSink starts the cutter and the transcription worker. The caller
// feeds it audio; nothing here touches the microphone.
func newListenSink(tr transcriber.Transcriber, lang, hints string) (*listenSink, error) {
	dir := config.Dir()
	if dir == "" {
		return nil, fmt.Errorf("no config directory for the transcript")
	}
	vp, err := newVADProcessor()
	if err != nil {
		return nil, fmt.Errorf("VAD init: %w", err)
	}
	s := &listenSink{
		tr:        tr,
		lang:      lang,
		hints:     hints,
		path:      filepath.Join(dir, listenTranscriptFile),
		vp:        vp,
		queue:     make(chan listenChunkJob, 256), // ~1 h of backlog at these chunk sizes
		updates:   make(chan string),
		stop:      make(chan struct{}),
		cutterEnd: make(chan struct{}),
		workerEnd: make(chan struct{}),
	}
	// One header per session, so a single file can hold several meetings and
	// stay readable. The file is append-only by design and never truncated: a
	// lost meeting is worse than a long file.
	s.append(fmt.Sprintf("\n=== listen session %s (%v-%v chunks) ===\n",
		time.Now().Format("2006-01-02 15:04:05"), listenMinChunk, listenMaxChunk))
	go s.worker()
	go s.cutter()
	log.Info(fmt.Sprintf("listen_start min_s=%.0f max_s=%.0f silence_ms=%d path=%s",
		listenMinChunk.Seconds(), listenMaxChunk.Seconds(),
		listenMinSilence.Milliseconds(), s.path))
	return s, nil
}

// Feed takes raw PCM from the recording session's capture callback. It copies:
// the caller reuses its buffer after Feed returns.
func (s *listenSink) Feed(pcm []byte) {
	buf := make([]byte, len(pcm))
	copy(buf, pcm)
	s.mu.Lock()
	s.pcm = append(s.pcm, buf...)
	s.mu.Unlock()
	s.vp.Process(buf) // drives the silence detection the cutter reads
}

// Updates satisfies the interface; listen mode emits no streaming partials, so
// the channel only ever closes.
func (s *listenSink) Updates() <-chan string { return s.updates }

// buffered reports how much audio is waiting to be cut.
func (s *listenSink) buffered() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Duration(len(s.pcm)) * time.Second / (encoder.SampleRate * 2)
}

// take removes and returns everything buffered so far.
func (s *listenSink) take() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	buf := s.pcm
	s.pcm = nil
	return buf
}

// cutter decides chunk boundaries. It never transcribes, so a slow inference
// cannot stall cut decisions or the capture callback behind them.
func (s *listenSink) cutter() {
	defer close(s.cutterEnd)
	ticker := time.NewTicker(listenCutPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			switch dur := s.buffered(); {
			case dur >= listenMaxChunk:
				s.cut("maxlen")
			case dur >= listenMinChunk && !s.vp.SpeakingNow(listenMinSilence):
				s.cut("silence")
			}
		case <-s.stop:
			s.cut("stop") // tail: whatever arrived since the last cut
			return
		}
	}
}

// cut moves buffered audio to the queue. Chunks with no speech are dropped
// rather than queued: whisper invents text on pure silence (phantom "Thank
// you." and subtitle credits), which would fill a quiet meeting's transcript
// with words nobody said.
func (s *listenSink) cut(reason string) {
	raw := s.take()
	if len(raw) == 0 {
		return
	}
	if _, speech := s.vp.StatsDelta(); speech == 0 {
		log.Info(fmt.Sprintf("listen_skip reason=%s audio_s=%.1f cause=no_speech",
			reason, float64(len(raw))/2/float64(encoder.SampleRate)))
		return
	}
	s.queue <- listenChunkJob{pcm: raw, reason: reason, cutAt: time.Now()}
}

// worker drains the queue serially: the engine serializes inference anyway,
// and the transcript must stay in order.
func (s *listenSink) worker() {
	defer close(s.workerEnd)
	for job := range s.queue {
		s.transcribe(job)
	}
}

// transcribe decodes one chunk and appends it. Errors are logged and the
// worker continues — one failed chunk must not end a meeting.
func (s *listenSink) transcribe(job listenChunkJob) {
	start := time.Now()
	sess, err := s.tr.NewSession(context.Background(), transcriber.SessionConfig{
		Format:   "wav",
		Language: s.lang,
		Hints:    s.hints,
	})
	if err != nil {
		log.Errorf("listen: session: %v", err)
		return
	}
	sess.Feed(job.pcm)
	res, err := sess.Close()
	if err != nil {
		log.Errorf("listen: transcribe: %v", err)
		return
	}
	// queue is the backlog depth, lag_ms how long the chunk waited before its
	// inference began. Both climbing together means transcription is losing to
	// real time — the signal to widen the bounds or pick a faster model.
	log.Info(fmt.Sprintf("listen_chunk audio_s=%.1f cut=%s inference_ms=%.0f lag_ms=%.0f queue=%d chars=%d",
		float64(len(job.pcm))/2/float64(encoder.SampleRate), job.reason,
		float64(time.Since(start).Microseconds())/1000,
		float64(time.Since(job.cutAt).Microseconds())/1000, len(s.queue), len(res.Text)))
	if res.Text == "" {
		return
	}
	s.written++
	s.append(fmt.Sprintf("[%s] %s\n", time.Now().Format("15:04:05"), res.Text))
}

// append adds one line, reopening the file each time so an interrupted session
// (crash, force quit) still leaves everything written so far on disk.
func (s *listenSink) append(line string) {
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Errorf("listen: cannot write %s: %v", s.path, err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		log.Errorf("listen: write: %v", err)
	}
}

// Close cuts the tail and waits for the queue to drain, so the transcript is
// complete when it returns. A long backlog therefore makes Close slow — right,
// since the alternative is discarding speech already recorded.
func (s *listenSink) Close() (transcriber.SessionResult, error) {
	close(s.updates)
	s.stopOnce.Do(func() { close(s.stop) })
	<-s.cutterEnd  // tail queued
	close(s.queue) // no more jobs
	<-s.workerEnd  // every queued chunk transcribed
	s.append(fmt.Sprintf("=== end %s ===\n", time.Now().Format("15:04:05")))
	log.Info(fmt.Sprintf("listen_stop chunks=%d path=%s", s.written, s.path))
	// No text is returned on purpose: listen mode's output is the file, and a
	// transcript here would be copied to the clipboard and pasted.
	return transcriber.SessionResult{NoSpeech: true}, nil
}

// setListenMode is the tray handler: a persisted setting, like auto-paste.
// Nothing starts or stops here — the next recording picks it up.
func setListenMode(on bool) {
	configMu.Lock()
	listenMode = on
	configMu.Unlock()
	config.Update(func(c *config.Settings) { c.ListenMode = on })
	tray.SetListen(on)
	log.Info(fmt.Sprintf("listen_mode on=%v", on))
}
