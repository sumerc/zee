package hotkey

import (
    "time"
)

type Mode string

const (
    ModePTT    Mode = "ptt"
    ModeToggle Mode = "toggle"
)

// StartEvent indicates a new recording should start with the given mode.
type StartEvent struct {
    Mode Mode
}

// Hybrid wraps a Hotkey to provide hybrid tap-to-toggle and hold-to-talk behavior
// using the same key combination. It emits Start events and a unified Stop channel
// that signals when recording should end (for both PTT and Toggle modes).
type Hybrid struct {
    startCh chan StartEvent
    stopCh  chan struct{}
}

// NewHybrid builds a Hybrid controller on top of an existing Hotkey.
// longPress specifies the duration threshold to treat a press as PTT vs tap.
func NewHybrid(hk Hotkey, longPress time.Duration) *Hybrid {
    h := &Hybrid{
        startCh: make(chan StartEvent, 1),
        stopCh:  make(chan struct{}, 1),
    }
    go h.run(hk, longPress)
    return h
}

// Start returns a channel of StartEvent values signaling when to begin recording.
func (h *Hybrid) Start() <-chan StartEvent { return h.startCh }

// StopChan returns a channel that is signaled when to stop recording
// (used for both PTT and toggle modes).
func (h *Hybrid) StopChan() <-chan struct{} { return h.stopCh }

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
            // Any press starts immediately; mode is decided by hold duration.
            <-hk.Keydown()
            // Start now; actual long/short distinction only changes when we stop.
            h.startCh <- StartEvent{Mode: ModeToggle}
            timer := time.NewTimer(longPress)
            select {
            case <-timer.C:
                // Treated as hold: stop on release
                <-hk.Keyup()
                select { case h.stopCh <- struct{}{}: default: }
                state = stIdle
            case <-hk.Keyup():
                // Short tap: toggled on; wait next press to stop
                if !timer.Stop() { select { case <-timer.C: default: } }
                state = stToggleRecording
            }
        case stToggleRecording:
            // Next press will stop on its release (short or long)
            <-hk.Keydown()
            <-hk.Keyup()
            select { case h.stopCh <- struct{}{}: default: }
            state = stIdle
        default:
            state = stIdle
        }
    }
}
