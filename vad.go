package main

import (
	"sync"
	"time"

	webrtcvad "github.com/maxhawkins/go-webrtcvad"
	"zee/encoder"
)

const (
	vadMode       = 3
	vadFrameMs    = 20
	vadFrameBytes = encoder.SampleRate * vadFrameMs / 1000 * 2 // 640 bytes
	vadDebounce   = 3                                          // consecutive speech frames to confirm voice
)

type vadProcessor struct {
	vad *webrtcvad.VAD

	mu            sync.Mutex
	buf           []byte
	voiceDetected bool
	lastVoiceTime time.Time
	speechRun     int
	totalFrames   int
	speechFrames  int
	lastTotal     int
	lastSpeech    int
	tickTotal     int
	tickSpeech    int
}

func newVADProcessor() (*vadProcessor, error) {
	v, err := webrtcvad.New()
	if err != nil {
		return nil, err
	}
	if err := v.SetMode(vadMode); err != nil {
		return nil, err
	}
	return &vadProcessor{vad: v}, nil
}

func (p *vadProcessor) Process(data []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.buf = append(p.buf, data...)
	for len(p.buf) >= vadFrameBytes {
		frame := p.buf[:vadFrameBytes]
		p.buf = p.buf[vadFrameBytes:]

		active, err := p.vad.Process(encoder.SampleRate, frame)
		if err != nil {
			continue
		}
		p.totalFrames++
		if active {
			p.speechFrames++
			p.speechRun++
			if p.voiceDetected {
				p.lastVoiceTime = time.Now()
			} else if p.speechRun >= vadDebounce {
				p.voiceDetected = true
				p.lastVoiceTime = time.Now()
			}
		} else {
			p.speechRun = 0
		}
	}
}

func (p *vadProcessor) VoiceDetected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.voiceDetected
}

func (p *vadProcessor) LastVoiceTime() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastVoiceTime
}

func (p *vadProcessor) Stats() (total, speech int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.totalFrames, p.speechFrames
}

func (p *vadProcessor) StatsDelta() (total, speech int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	t, s := p.totalFrames-p.lastTotal, p.speechFrames-p.lastSpeech
	p.lastTotal, p.lastSpeech = p.totalFrames, p.speechFrames
	return t, s
}

const speechThreshold = 0.10 // 10% of frames must be speech to count as "speaking"

func (p *vadProcessor) HasSpeechTick() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.totalFrames - p.tickTotal
	s := p.speechFrames - p.tickSpeech
	p.tickTotal, p.tickSpeech = p.totalFrames, p.speechFrames
	if t == 0 {
		return false
	}
	return float64(s)/float64(t) >= speechThreshold
}

func (p *vadProcessor) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buf = p.buf[:0]
	p.voiceDetected = false
	p.lastVoiceTime = time.Time{}
	p.speechRun = 0
}
