package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"zee/audio"
	"zee/log"
	"zee/transcriber"
	"zee/tray"
)

type recordingSession struct {
	capture         audio.CaptureDevice
	transcriberSess transcriber.Session
	stop            <-chan struct{}
	vp              *vadProcessor
	mon             *silenceMonitor
	tailWait        time.Duration // mic stays open this long after release (anti-clip)

	mu          sync.Mutex
	totalFrames uint64
	stopped     bool
	autoClosed  atomic.Bool
	// releasedAt (unix nanos) is when the user stopped talking — hotkey release,
	// or the silence auto-close, whichever ended the recording. It is the start
	// of the latency the user actually feels, so everything after it (tail wait,
	// device stop, encode, inference, paste) is measured from here. Atomic
	// because awaitStop and monitorSilence can reach the stop path concurrently.
	releasedAt atomic.Int64
	done       chan struct{}
	closeOnce  sync.Once
}

func newRecordingSession(capture audio.CaptureDevice, stop <-chan struct{}, sess transcriber.Session, silenceClose *atomic.Bool, tailWait time.Duration) (*recordingSession, error) {
	vp, err := newVADProcessor()
	if err != nil {
		return nil, fmt.Errorf("VAD init: %w", err)
	}
	return &recordingSession{
		capture:         capture,
		transcriberSess: sess,
		stop:            stop,
		vp:              vp,
		mon:             newSilenceMonitor(silenceClose),
		tailWait:        tailWait,
		done:            make(chan struct{}),
	}, nil
}

func (r *recordingSession) onAudio(data []byte, frameCount uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	r.totalFrames += uint64(frameCount)

	if len(data) > 0 {
		r.transcriberSess.Feed(data)
		r.vp.Process(data)
	}
}

func (r *recordingSession) Start() error {
	r.capture.SetCallback(r.onAudio)
	if err := r.capture.Start(); err != nil {
		r.capture.ClearCallback()
		return err
	}
	go r.monitorSilence()
	go r.awaitStop()
	return nil
}

func (r *recordingSession) monitorSilence() {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			switch r.mon.Tick(r.vp.HasSpeechTick()) {
			case SilenceWarn:
				log.Info("no_voice_warning")
				tray.SetWarning(true)
				audio.PlayError()
			case SilenceWarnClear:
				tray.SetWarning(false)
			case SilenceRepeat:
				log.Info("silence_during_warning")
				audio.PlayError()
			case SilenceAutoClose:
				r.markReleased()
				log.Info("silence_auto_close")
				audio.PlayEnd()
				tray.SetRecording(false)
				r.autoClosed.Store(true)
				r.close()
				return
			}
		}
	}
}

func (r *recordingSession) awaitStop() {
	select {
	case <-r.stop:
	case <-r.done:
		return
	}
	r.markReleased()
	log.Info("recording_stop")
	audio.PlayEnd() // reflexive: sound the release before the tray/icon update (playOne is non-blocking)
	tray.SetRecording(false)
	// Keep the mic open a beat after release so a fast keyup doesn't clip the
	// last word: onAudio keeps feeding until close() below stops capture.
	if r.tailWait > 0 {
		time.Sleep(r.tailWait)
	}
	r.close()
}

func (r *recordingSession) close() {
	r.closeOnce.Do(func() { close(r.done) })
}

// markReleased stamps the moment recording ended, first writer wins: both stop
// paths can run, and the earlier one is the instant the user stopped talking.
func (r *recordingSession) markReleased() {
	r.releasedAt.CompareAndSwap(0, time.Now().UnixNano())
}

// ReleasedAt reports when recording ended, or the zero time if it never did.
func (r *recordingSession) ReleasedAt() time.Time {
	ns := r.releasedAt.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

func (r *recordingSession) Wait() {
	<-r.done

	r.capture.Stop()
	r.capture.ClearCallback()

	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
}
