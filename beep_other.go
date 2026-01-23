//go:build !linux

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
)

var (
	otoCtx      *oto.Context
	startBuffer []byte
	endBuffer   []byte
	soundOnce   sync.Once
)

func initSound() {
	var err error
	var ready chan struct{}
	otoCtx, ready, err = oto.NewContext(&oto.NewContextOptions{
		SampleRate:   44100,
		ChannelCount: 1,
		Format:       oto.FormatSignedInt16LE,
		BufferSize:   50 * time.Millisecond,
	})
	if err != nil {
		logDiagError(fmt.Sprintf("oto init error: %v", err))
		return
	}
	<-ready

	// Start sound: snappy tick (30ms, fast decay)
	startBuffer = generateTick(44100, 1200, 0.03, 0.5, 60)
	// End sound: slightly lower tick (50ms, moderate decay)
	endBuffer = generateTick(44100, 900, 0.05, 0.5, 40)
}

func generateTick(sampleRate int, freq float64, duration float64, volume float64, decay float64) []byte {
	samples := int(float64(sampleRate) * duration)
	buf := new(bytes.Buffer)
	for i := 0; i < samples; i++ {
		t := float64(i) / float64(sampleRate)
		envelope := math.Exp(-t * decay)
		sample := int16(math.Sin(2*math.Pi*freq*t) * 32767 * volume * envelope)
		binary.Write(buf, binary.LittleEndian, sample)
	}
	return buf.Bytes()
}

func playStartSound() {
	soundOnce.Do(initSound)
	if otoCtx == nil || len(startBuffer) == 0 {
		return
	}
	player := otoCtx.NewPlayer(bytes.NewReader(startBuffer))
	player.Play()
	go func() {
		for player.IsPlaying() {
		}
		player.Close()
	}()
}

func playEndSound() {
	soundOnce.Do(initSound)
	if otoCtx == nil || len(endBuffer) == 0 {
		return
	}
	player := otoCtx.NewPlayer(bytes.NewReader(endBuffer))
	player.Play()
	go func() {
		for player.IsPlaying() {
		}
		player.Close()
	}()
}
