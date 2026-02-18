package main

import "time"

const (
	tickInterval        = 100 * time.Millisecond
	silenceWarnEvery    = 8 * time.Second
	silenceAutoCloseDur = 30 * time.Second
	speechMinRatio      = 0.10
	speechClearRatio    = 0.25 // higher threshold to clear warning (hysteresis)
)

type SilenceEvent int

const (
	SilenceNone      SilenceEvent = iota
	SilenceWarn                   // no voice detected
	SilenceWarnClear              // speech resumed after warning
	SilenceRepeat                 // repeat beep (every 8s)
	SilenceAutoClose              // 30s auto-close (toggle mode)
)

type silenceMonitor struct {
	warnAt   int
	windowSz int

	isToggle func() bool

	ticks       int
	window      []bool
	speechCount int
	warned      bool
	lastBeep    int
}

func newSilenceMonitor(isToggle func() bool) *silenceMonitor {
	warnAt := int(silenceWarnEvery / tickInterval)
	windowSz := int(silenceAutoCloseDur / tickInterval)
	return &silenceMonitor{
		warnAt:   warnAt,
		windowSz: windowSz,
		isToggle: isToggle,
		window:   make([]bool, windowSz),
	}
}

func (m *silenceMonitor) ratio(n int) float64 {
	if m.ticks < n {
		n = m.ticks
	}
	if n == 0 {
		return 1.0
	}
	count := 0
	for i := 0; i < n; i++ {
		if m.window[(m.ticks-1-i+m.windowSz)%m.windowSz] {
			count++
		}
	}
	return float64(count) / float64(n)
}

func (m *silenceMonitor) Tick(hasSpeech bool) SilenceEvent {
	idx := m.ticks % m.windowSz
	if m.ticks >= m.windowSz && m.window[idx] {
		m.speechCount--
	}
	m.window[idx] = hasSpeech
	if hasSpeech {
		m.speechCount++
	}
	m.ticks++

	r := m.ratio(m.warnAt)

	// Warn: 8s window below threshold
	if m.ticks >= m.warnAt && r < speechMinRatio && !m.warned {
		m.warned = true
		m.lastBeep = m.ticks
		return SilenceWarn
	}
	// Clear: speech ratio above clear threshold
	if m.warned && r >= speechClearRatio {
		m.warned = false
		return SilenceWarnClear
	}

	if !m.isToggle() {
		return SilenceNone
	}

	// Auto-close: 30s window below threshold (checked before repeat)
	if m.ticks >= m.windowSz && float64(m.speechCount)/float64(m.windowSz) < speechMinRatio {
		return SilenceAutoClose
	}

	// Repeat beep every 8s
	if m.warned && m.ticks-m.lastBeep >= m.warnAt {
		m.lastBeep = m.ticks
		return SilenceRepeat
	}

	return SilenceNone
}
