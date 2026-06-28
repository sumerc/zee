package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zee/audio"
	"zee/beep"
	"zee/encoder"
	"zee/hotkey"
	"zee/transcriber"
)

// TestRecordSessionsBlocksDuringInference verifies the guard's missing half:
// isRecording must stay true for the WHOLE record+transcribe cycle, not just
// while recording. It drives the real recordSessions loop with a fake capture
// and a fake transcriber whose "inference" takes 800ms, then checks isRecording
// is still set mid-inference. Combined with TestListenHotkey_StopsTrayRecording
// (a press while isRecording is true starts no new session), this proves a
// hotkey press during inference is blocked.
func TestRecordSessionsBlocksDuringInference(t *testing.T) {
	beep.Disable()
	isRecording.Store(false)

	fake := transcriber.NewFake("hello", nil)
	fake.SetDelay(800 * time.Millisecond) // simulated inference window
	activeTranscriber = fake

	ctx, err := audio.NewFakeContext("test/data/short.wav", false)
	if err != nil {
		t.Fatalf("fake audio context: %v", err)
	}
	capture, err := ctx.NewCapture(nil, audio.CaptureConfig{
		SampleRate: encoder.SampleRate, Channels: encoder.Channels,
	})
	if err != nil {
		t.Fatalf("fake capture: %v", err)
	}
	defer capture.Close()

	var cycles int32
	afterRecordCycle = func() { atomic.AddInt32(&cycles, 1) }
	defer func() { afterRecordCycle = nil }()

	sessions := make(chan recSession, 1)
	loopDone := make(chan struct{})
	go func() { recordSessions(func() audio.CaptureDevice { return capture }, sessions); close(loopDone) }()

	// Start a recording, let it capture briefly, then stop it (as a keyup would)
	// so the 800ms "inference" begins.
	sessions <- recSession{Stop: resetStop(), SilenceClose: &atomic.Bool{}}
	time.Sleep(150 * time.Millisecond)
	requestStop()

	// Mid-inference: the guard must still be engaged, and isTranscribing (which
	// drives the blue icon + denied beep) must be set.
	time.Sleep(250 * time.Millisecond)
	if !isRecording.Load() {
		t.Fatal("isRecording cleared during inference — a re-record would NOT be blocked")
	}
	if !isTranscribing.Load() {
		t.Fatal("isTranscribing not set during inference — no blue icon / denied beep")
	}

	// Wait out the cycle, confirm both flags were released after inference.
	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt32(&cycles) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("record cycle never completed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if isRecording.Load() || isTranscribing.Load() {
		t.Fatal("isRecording/isTranscribing still set after inference completed")
	}

	// Terminate the loop cleanly so it doesn't leak into other tests.
	close(sessions)
	<-loopDone
}

// TestRecordSessionsPicksUpDeviceSwitch reproduces the "Start auto-releases"
// bug: when the selected mic is unplugged mid-run, the device monitor swaps the
// capture device to system default, but recordSessions kept using a frozen
// reference to the old (now-gone) device — so every recording aborted with
// "device reinit failed: No device". recordSessions must read the current
// device (via getCapture) each iteration. With the old by-value behavior the
// post-swap session still uses device A and this test fails.
func TestRecordSessionsPicksUpDeviceSwitch(t *testing.T) {
	beep.Disable()
	isRecording.Store(false)

	activeTranscriber = transcriber.NewFake("hello", nil)

	ctx, err := audio.NewFakeContext("test/data/short.wav", false)
	if err != nil {
		t.Fatalf("fake audio context: %v", err)
	}
	capA, _ := ctx.NewNamedCapture("A")
	capB, _ := ctx.NewNamedCapture("B")
	fa := capA.(*audio.FakeCapture)
	fb := capB.(*audio.FakeCapture)

	var mu sync.Mutex
	current := capA
	getCapture := func() audio.CaptureDevice {
		mu.Lock()
		defer mu.Unlock()
		return current
	}

	var cycles int32
	afterRecordCycle = func() { atomic.AddInt32(&cycles, 1) }
	defer func() { afterRecordCycle = nil }()

	sessions := make(chan recSession, 1)
	loopDone := make(chan struct{})
	go func() { recordSessions(getCapture, sessions); close(loopDone) }()

	runSession := func() {
		want := atomic.LoadInt32(&cycles) + 1
		sessions <- recSession{Stop: resetStop(), SilenceClose: &atomic.Bool{}}
		time.Sleep(120 * time.Millisecond)
		requestStop()
		deadline := time.Now().Add(3 * time.Second)
		for atomic.LoadInt32(&cycles) < want {
			if time.Now().After(deadline) {
				t.Fatalf("record cycle %d never completed", want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	runSession() // session 1 → device A

	mu.Lock() // mic unplugged: monitor swapped to device B
	current = capB
	mu.Unlock()

	runSession() // session 2 → must be device B, not the stale A

	close(sessions)
	<-loopDone

	if fb.Starts.Load() == 0 {
		t.Fatal("device switch ignored: the session after the swap did not use the new device")
	}
	if fa.Starts.Load() != 1 {
		t.Fatalf("stale device A was used %d times, want 1 (only the pre-swap session)", fa.Starts.Load())
	}
}

func TestListenHotkey_TrayStopNoStaleSignal(t *testing.T) {
	hk := hotkey.NewFake()
	sessions := make(chan recSession, 3)
	longPress := 100 * time.Millisecond

	go listenHotkey(hk, longPress, sessions)

	// 1. Short tap → enters toggle mode
	isRecording.Store(false)
	hk.SimKeydown()
	sess1 := <-sessions
	isRecording.Store(true)
	time.Sleep(10 * time.Millisecond)
	hk.SimKeyup()

	// 2. Tray stop ends the recording externally
	requestStop()
	isRecording.Store(false)
	select {
	case <-sess1.Stop:
	case <-time.After(time.Second):
		t.Fatal("tray stop did not end session")
	}

	// 3. Still in toggleRecording — this tap transitions back to idle
	hk.SimKeydown()
	hk.SimKeyup()
	time.Sleep(20 * time.Millisecond) // let state machine settle

	// 4. New tap should start a session that stays alive
	hk.SimKeydown()
	sess2 := <-sessions
	isRecording.Store(true)
	time.Sleep(10 * time.Millisecond)
	hk.SimKeyup()

	select {
	case <-sess2.Stop:
		t.Fatal("new session immediately stopped by stale stopCh signal")
	case <-time.After(300 * time.Millisecond):
		// session stayed alive — fix works
	}
}

func TestListenHotkey_StopsTrayRecording(t *testing.T) {
	hk := hotkey.NewFake()
	sessions := make(chan recSession, 3)
	longPress := 100 * time.Millisecond

	go listenHotkey(hk, longPress, sessions)

	// Simulate tray-initiated recording
	stop := resetStop()
	isRecording.Store(true)

	// Hotkey press should stop it, not start a new one
	hk.SimKeydown()
	hk.SimKeyup()

	select {
	case <-stop:
	case <-time.After(time.Second):
		t.Fatal("hotkey did not stop tray-initiated recording")
	}

	// Should not have queued a new session
	select {
	case <-sessions:
		t.Fatal("hotkey started a new session while recording was active")
	case <-time.After(100 * time.Millisecond):
	}
}
