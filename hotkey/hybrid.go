package hotkey

import (
	"sync/atomic"
	"time"
)

type Mode string

const (
	ModePTT    Mode = "ptt"
	ModeToggle Mode = "toggle"
)

type StartEvent struct {
	Mode Mode
}

type Hybrid struct {
	startCh  chan StartEvent
	stopCh   chan struct{}
	isToggle atomic.Bool
}

func NewHybrid(hk Hotkey, longPress time.Duration) *Hybrid {
	h := &Hybrid{
		startCh: make(chan StartEvent, 1),
		stopCh:  make(chan struct{}, 1),
	}
	go h.run(hk, longPress)
	return h
}

func (h *Hybrid) Start() <-chan StartEvent { return h.startCh }
func (h *Hybrid) StopChan() <-chan struct{} { return h.stopCh }
func (h *Hybrid) IsToggle() bool           { return h.isToggle.Load() }

type hybridState int

const (
	stIdle hybridState = iota
	stToggleRecording
	stPTTRecording
)

func (h *Hybrid) run(hk Hotkey, longPress time.Duration) {
	state := stIdle
	for {
		switch state {
		case stIdle:
			<-hk.Keydown()
			h.isToggle.Store(false) // reset for each recording
			h.startCh <- StartEvent{Mode: ModeToggle}
			timer := time.NewTimer(longPress)
			select {
			case <-timer.C:
				// Hold: PTT — isToggle stays false
				<-hk.Keyup()
				select { case h.stopCh <- struct{}{}: default: }
				state = stIdle
			case <-hk.Keyup():
				if !timer.Stop() { select { case <-timer.C: default: } }
				h.isToggle.Store(true)
				state = stToggleRecording
			}
		case stToggleRecording:
			<-hk.Keydown()
			<-hk.Keyup()
			select { case h.stopCh <- struct{}{}: default: }
			state = stIdle
		default:
			state = stIdle
		}
	}
}
