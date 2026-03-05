package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"zee/audio"
	"zee/beep"
	"zee/log"
	"zee/transcriber"
	"zee/tray"
)

const recordTail = 500 * time.Millisecond

type recordingSession struct {
	capture audio.CaptureDevice
	transcriberSess transcriber.Session
	stop    <-chan struct{}
	vp      *vadProcessor
	mon     *silenceMonitor
	stream  bool

	mu          sync.Mutex
	totalFrames uint64
	stopped     bool
	autoClosed  atomic.Bool
	done        chan struct{}
	closeOnce   sync.Once
}

func newRecordingSession(capture audio.CaptureDevice, stop <-chan struct{}, sess transcriber.Session, silenceClose *atomic.Bool, stream bool) (*recordingSession, error) {
	vp, err := newVADProcessor()
	if err != nil {
		return nil, fmt.Errorf("VAD init: %w", err)
	}
	return &recordingSession{
		capture: capture,
		transcriberSess:    sess,
		stop:    stop,
		vp:      vp,
		mon:     newSilenceMonitor(silenceClose),
		stream:  stream,
		done:    make(chan struct{}),
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
				beep.PlayError()
			case SilenceWarnClear:
				tray.SetWarning(false)
			case SilenceRepeat:
				log.Info("silence_during_warning")
				beep.PlayError()
			case SilenceAutoClose:
				log.Info("silence_auto_close")
				tray.SetRecording(false)
				go beep.PlayEnd()
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
	log.Info("recording_stop")
	tray.SetRecording(false)
	go beep.PlayEnd()
	if r.stream {
		time.Sleep(recordTail)
	}
	r.close()
}

func (r *recordingSession) close() {
	r.closeOnce.Do(func() { close(r.done) })
}

func (r *recordingSession) Wait() {
	<-r.done

	r.capture.Stop()
	r.capture.ClearCallback()

	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
}
