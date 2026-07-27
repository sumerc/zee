// Command overlaydemo shows the recording overlay on its own, driven by a fake
// microphone, so the look can be iterated on without running zee end to end.
//
//	go run ./cmd/overlaydemo
//
// It cycles Recording → Are you talking? → Transcribing every few seconds.
package main

import (
	"math"
	"math/rand"
	"runtime"
	"sync/atomic"
	"time"

	"zee/overlay"
)

func main() {
	runtime.LockOSThread()
	go drive()
	overlay.Run() // blocks: AppKit owns this thread from here on
}

func drive() {
	time.Sleep(300 * time.Millisecond)
	overlay.Show()

	var state atomic.Int32
	go func() {
		states := []overlay.State{overlay.Recording, overlay.Silent, overlay.Transcribing}
		for i := 0; ; i++ {
			s := states[i%len(states)]
			state.Store(int32(s))
			overlay.SetState(s)
			time.Sleep(3 * time.Second)
		}
	}()

	// Fake microphone: a slow envelope with per-frame jitter reads as speech,
	// where uniform noise just looks like static.
	t := 0.0
	for range time.Tick(33 * time.Millisecond) {
		t += 0.033
		var v float64
		switch overlay.State(state.Load()) {
		case overlay.Silent:
			v = rand.Float64() * 0.04
		case overlay.Transcribing:
			v = 0.12 + 0.10*math.Sin(t*6) // idle shimmer while we wait on text
		default:
			env := 0.35 + 0.65*math.Abs(math.Sin(t*1.7))
			v = env * (0.3 + rand.Float64()*0.7)
		}
		overlay.PushLevel(float32(v))
	}
}
