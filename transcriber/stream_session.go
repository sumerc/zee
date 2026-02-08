package transcriber

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"zee/encoder"
	"zee/log"
)

const (
	streamChunkMs      = 200
	streamChunkBytes   = encoder.SampleRate * encoder.Channels * (encoder.BitsPerSample / 8) * streamChunkMs / 1000
	streamFinalizeIdle = 200 * time.Millisecond
	streamFinalizeMax  = 1000 * time.Millisecond
)

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

	sendDone      chan struct{}
	recvDone      chan struct{}
	finalized     chan struct{}
	finalizedOnce sync.Once

	feedBuf []byte
	feedMu  sync.Mutex

	mu      sync.Mutex
	err     error
	errOnce sync.Once
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
		audioCh:   make(chan []byte, 128),
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
			ss.errOnce.Do(func() {
				ss.mu.Lock()
				ss.err = err
				ss.mu.Unlock()
			})
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
		s.audioCh <- chunk
	}
}

func (s *streamSession) Updates() <-chan string {
	return s.updates
}

func (s *streamSession) Close() (SessionResult, error) {
	<-s.connected

	// If connection failed, drain and return error
	s.mu.Lock()
	if s.err != nil {
		connErr := s.err
		s.mu.Unlock()
		go func() { // drain audioCh so any blocked Feed() unblocks
			for range s.audioCh {
			}
		}()
		s.feedMu.Lock()
		s.feedBuf = nil
		s.feedMu.Unlock()
		close(s.audioCh)
		<-s.sendDone
		<-s.recvDone
		close(s.updates)
		return SessionResult{NoSpeech: true}, connErr
	}
	s.mu.Unlock()

	// Flush remaining buffered PCM
	s.feedMu.Lock()
	if len(s.feedBuf) > 0 {
		tail := make([]byte, len(s.feedBuf))
		copy(tail, s.feedBuf)
		s.feedBuf = nil
		s.audioCh <- tail
	}
	s.feedMu.Unlock()
	close(s.audioCh)
	finalizeStart := time.Now()

	<-s.sendDone

	// Wait for server finalize acknowledgment, then brief quiet period
	select {
	case <-s.finalized:
		time.Sleep(streamFinalizeIdle)
	case <-time.After(streamFinalizeMax):
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
		select {
		case s.updates <- finalText:
		default:
		}
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
	sr.captureMemStats()
	return sr, sessionErr
}

func (s *streamSession) runSender() {
	defer close(s.sendDone)
	for chunk := range s.audioCh {
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

func (s *streamSession) setErr(err error) {
	if err == nil {
		return
	}
	s.errOnce.Do(func() {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		if s.ws != nil {
			s.ws.Close()
		}
	})
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
