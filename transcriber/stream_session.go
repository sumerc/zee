package transcriber

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"zee/audio"
	"zee/encoder"
	"zee/log"
)

const (
	streamChunkMs      = 200
	streamChunkBytes   = encoder.SampleRate * encoder.Channels * (encoder.BitsPerSample / 8) * streamChunkMs / 1000
	streamFinalizeIdle = 200 * time.Millisecond
	streamFinalizeMax  = 2000 * time.Millisecond
	// streamSendTimeout bounds one websocket write. A stalled network makes a
	// write block instead of fail, and a sender stuck there never drains audioCh.
	streamSendTimeout = 5 * time.Second
	// streamQueueChunks is how much audio may wait for the sender before Feed
	// gives up on the stream.
	streamQueueChunks = 128
)

// streamDrainMax bounds how long Close waits for the sender to flush the
// queued audio before it aborts the stream. A var so tests can shorten it.
var streamDrainMax = 5 * time.Second

// errStreamStalled is recorded when the sender falls a full queue behind:
// Feed runs on the capture callback and must never block on the network.
var errStreamStalled = errors.New("stream stalled: network not keeping up, audio kept for saving")

// rawStreamSession is one provider websocket. Close must unblock a Send or Recv
// in progress; streamSession relies on it to abort a stalled stream.
type rawStreamSession interface {
	Send(pcm []byte) error
	CloseSend() error
	Recv() (streamUpdate, error)
	Close() error
}

type streamUpdate struct {
	Transcript   string
	IsFinal      bool
	SpeechFinal  bool
	FromFinalize bool
}

type streamSession struct {
	ws        rawStreamSession
	committed string
	audioCh   chan []byte
	updates   chan string
	startedAt time.Time
	connected chan struct{} // closed when WebSocket is ready (or failed)
	// hints string // TODO: Deepgram streaming supports keywords param

	sendDone      chan struct{}
	recvDone      chan struct{}
	finalized     chan struct{}
	finalizedOnce sync.Once

	lang string // SessionResult.Lang; set by the provider before the session is used

	feedBuf []byte
	pcmBuf  []byte // full session PCM, returned as WAV in SessionResult.AudioData
	feedMu  sync.Mutex

	mu      sync.Mutex
	err     error
	closing bool
	stats   streamStats
}

type streamStats struct {
	ConnectDur   time.Duration
	SentChunks   int
	SentBytes    uint64
	RecvMessages int
	RecvFinal    int
	RecvInterim  int
	CommitEvents int
	FinalizeWait time.Duration
	SessionDur   time.Duration
}

func (s streamStats) audioDuration() float64 {
	return float64(s.SentBytes) / float64(encoder.SampleRate*encoder.Channels*(encoder.BitsPerSample/8))
}

func newStreamSession(dial func() (rawStreamSession, error)) *streamSession {
	ss := &streamSession{
		audioCh:   make(chan []byte, streamQueueChunks),
		updates:   make(chan string, 16),
		startedAt: time.Now(),
		sendDone:  make(chan struct{}),
		recvDone:  make(chan struct{}),
		finalized: make(chan struct{}),
		connected: make(chan struct{}),
	}

	go func() {
		connectStart := time.Now()
		ws, err := dial()
		ss.mu.Lock()
		ss.stats.ConnectDur = time.Since(connectStart)
		ss.mu.Unlock()

		if err != nil {
			ss.recordErr(err)
			close(ss.sendDone)
			close(ss.recvDone)
			close(ss.connected)
			return
		}

		ss.ws = ws
		close(ss.connected)
		go ss.runSender()
		go ss.runReceiver()
	}()

	return ss
}

func (s *streamSession) Feed(pcm []byte) {
	// Retain all PCM regardless of stream health — a broken stream is exactly
	// when the audio must survive, so main.go's save/auto-save-on-error paths
	// (SessionResult.AudioData) can persist the dictation.
	s.feedMu.Lock()
	s.pcmBuf = append(s.pcmBuf, pcm...)
	s.feedMu.Unlock()

	s.mu.Lock()
	if s.err != nil {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	s.feedMu.Lock()
	s.feedBuf = append(s.feedBuf, pcm...)
	var chunks [][]byte
	for len(s.feedBuf) >= streamChunkBytes {
		chunk := make([]byte, streamChunkBytes)
		copy(chunk, s.feedBuf[:streamChunkBytes])
		s.feedBuf = s.feedBuf[streamChunkBytes:]
		chunks = append(chunks, chunk)
	}
	s.feedMu.Unlock()

	for _, chunk := range chunks {
		if !s.enqueue(chunk) {
			return
		}
	}
}

// enqueue hands a chunk to the sender without blocking. A full queue means the
// sender is stuck on the network: the stream is failed rather than letting the
// capture callback (and the recording lock it holds) wait on it.
func (s *streamSession) enqueue(chunk []byte) bool {
	select {
	case s.audioCh <- chunk:
		return true
	default:
		s.recordErr(errStreamStalled)
		return false
	}
}

func (s *streamSession) Updates() <-chan string {
	return s.updates
}

func (s *streamSession) Close() (SessionResult, error) {
	<-s.connected

	// The dial failed: sender and receiver never ran.
	if s.ws == nil {
		close(s.updates)
		sr := SessionResult{NoSpeech: true}
		s.attachAudio(&sr)
		return sr, s.sessionErr()
	}

	// Flush remaining buffered PCM
	s.feedMu.Lock()
	if len(s.feedBuf) > 0 && s.sessionErr() == nil {
		tail := make([]byte, len(s.feedBuf))
		copy(tail, s.feedBuf)
		s.enqueue(tail)
	}
	s.feedBuf = nil
	s.feedMu.Unlock()
	close(s.audioCh)
	finalizeStart := time.Now()

	// A failed stream has nothing left worth sending: abort it now, which also
	// unblocks a write stuck on the network. A healthy one gets streamDrainMax
	// to flush its queue.
	if s.sessionErr() != nil {
		s.ws.Close()
	}
	select {
	case <-s.sendDone:
	case <-time.After(streamDrainMax):
		s.recordErr(fmt.Errorf("stream send did not finish in %s", streamDrainMax))
		s.ws.Close()
		select {
		case <-s.sendDone:
		case <-time.After(2 * time.Second):
			log.Warn("stream sender drain timeout")
		}
	}

	// Wait for server finalize acknowledgment, then brief quiet period. A
	// failed stream never gets one.
	if s.sessionErr() == nil {
		select {
		case <-s.finalized:
			time.Sleep(streamFinalizeIdle)
		case <-time.After(streamFinalizeMax):
		}
	}

	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	s.ws.Close()
	select {
	case <-s.recvDone:
	case <-time.After(2 * time.Second):
		log.Warn("stream receiver drain timeout")
	}

	// Guarantee consumer sees final text even if last non-blocking send was dropped
	s.mu.Lock()
	finalText := s.committed
	s.mu.Unlock()
	if finalText != "" {
		s.updates <- finalText
	}
	close(s.updates)

	s.mu.Lock()
	text := s.committed
	stats := s.stats
	stats.FinalizeWait = time.Since(finalizeStart)
	stats.SessionDur = time.Since(s.startedAt)
	sessionErr := s.err
	s.mu.Unlock()

	cleanText := strings.TrimSpace(text)
	noSpeech := cleanText == ""

	metrics := s.formatMetrics(stats)

	audioDuration := stats.audioDuration()

	sr := SessionResult{
		Lang:     s.lang,
		Text:     cleanText,
		HasText:  !noSpeech,
		NoSpeech: noSpeech,
		Metrics:  metrics,
		Stream: &StreamStats{
			ConnectMs:    float64(stats.ConnectDur.Milliseconds()),
			SentChunks:   stats.SentChunks,
			SentKB:       float64(stats.SentBytes) / 1024,
			RecvMessages: stats.RecvMessages,
			RecvFinal:    stats.RecvFinal,
			RecvInterim:  stats.RecvInterim,
			CommitEvents: stats.CommitEvents,
			FinalizeMs:   float64(stats.FinalizeWait.Milliseconds()),
			TotalMs:      float64(stats.SessionDur.Milliseconds()),
			AudioS:       audioDuration,
		},
	}
	s.attachAudio(&sr)
	sr.captureRSS()
	return sr, sessionErr
}

// attachAudio hands the retained session PCM back as a saveable WAV, matching
// the batch/local sessions' AudioData contract.
func (s *streamSession) attachAudio(sr *SessionResult) {
	s.feedMu.Lock()
	pcm := s.pcmBuf
	s.feedMu.Unlock()
	if len(pcm) == 0 {
		return
	}
	sr.AudioData = audio.PCMToWAV(pcm)
	sr.AudioFormat = "wav"
}

func (s *streamSession) runSender() {
	defer close(s.sendDone)
	for chunk := range s.audioCh {
		if s.sessionErr() != nil {
			return // the stream already failed; the queued audio is moot
		}
		if err := s.ws.Send(chunk); err != nil {
			s.setErr(err)
			return
		}
		s.mu.Lock()
		s.stats.SentChunks++
		s.stats.SentBytes += uint64(len(chunk))
		s.mu.Unlock()
	}
	if err := s.ws.CloseSend(); err != nil {
		s.setErr(err)
	}
}

func (s *streamSession) runReceiver() {
	defer close(s.recvDone)
	for {
		update, err := s.ws.Recv()
		if err != nil {
			s.mu.Lock()
			closing := s.closing
			s.mu.Unlock()
			if closing {
				return
			}
			s.setErr(err)
			return
		}

		if update.FromFinalize {
			s.finalizedOnce.Do(func() { close(s.finalized) })
		}

		isFinal := update.IsFinal || update.SpeechFinal || update.FromFinalize

		s.mu.Lock()
		s.stats.RecvMessages++
		if isFinal {
			s.stats.RecvFinal++
		} else {
			s.stats.RecvInterim++
		}
		s.mu.Unlock()

		if !isFinal {
			continue
		}

		transcript := strings.TrimSpace(update.Transcript)
		if transcript == "" {
			continue
		}

		s.mu.Lock()
		if s.committed != "" {
			s.committed += " " + transcript
		} else {
			s.committed = transcript
		}
		s.stats.CommitEvents++
		fullText := s.committed
		s.mu.Unlock()

		select {
		case s.updates <- fullText:
		default:
		}
	}
}

// setErr records a sender/receiver failure and closes the websocket so the
// other goroutine unblocks. Only those goroutines call it, so ws is set.
func (s *streamSession) setErr(err error) {
	if err != nil && s.recordErr(err) {
		s.ws.Close()
	}
}

// recordErr keeps the first error and reports whether this call set it.
func (s *streamSession) recordErr(err error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return false
	}
	s.err = err
	return true
}

func (s *streamSession) sessionErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *streamSession) formatMetrics(stats streamStats) []string {
	audioDuration := stats.audioDuration()

	return []string{
		fmt.Sprintf("audio:      %.1fs | %.1f KB PCM sent", audioDuration, float64(stats.SentBytes)/1024),
		fmt.Sprintf("stream:     deepgram | PCM16 %dHz mono | %dms chunks", encoder.SampleRate, streamChunkMs),
		fmt.Sprintf("connect:    %dms", stats.ConnectDur.Milliseconds()),
		fmt.Sprintf("sent:       %d chunks | %.1f KB", stats.SentChunks, float64(stats.SentBytes)/1024),
		fmt.Sprintf("recv:       %d msgs (%d final, %d interim)", stats.RecvMessages, stats.RecvFinal, stats.RecvInterim),
		fmt.Sprintf("commit:     %d updates", stats.CommitEvents),
		fmt.Sprintf("finalize:   %dms", stats.FinalizeWait.Milliseconds()),
		fmt.Sprintf("total:      %dms", stats.SessionDur.Milliseconds()),
	}
}
