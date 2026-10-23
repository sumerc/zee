package main

import (
	"errors"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zee/audio"
	"zee/config"
	"zee/encoder"
	"zee/hotkey"
	"zee/overlay"
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
	audio.DisableBeep()
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
	sessions <- recSession{Stop: resetStop(), SilenceClose: &atomic.Bool{}, Ended: make(chan struct{})}
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
	audio.DisableBeep()
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
		sessions <- recSession{Stop: resetStop(), SilenceClose: &atomic.Bool{}, Ended: make(chan struct{})}
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

// TestListenHotkey_TrayStopNoStaleSignal: a toggle recording ended from the
// tray must leave the hotkey ready — the very next tap starts a new session
// (it used to be eaten as the stop tap of the finished one), and that session
// is not stopped by a stale signal from the old one.
func TestListenHotkey_TrayStopNoStaleSignal(t *testing.T) {
	hk := hotkey.NewFake()
	sessions := make(chan recSession, 3)
	longPress := 100 * time.Millisecond

	go listenHotkey(hk, longPress, sessions)

	// 1. Short tap → enters toggle mode
	isRecording.Store(false)
	hk.SimKeydown()
	sess1 := <-sessions
	time.Sleep(10 * time.Millisecond)
	hk.SimKeyup()

	// 2. Tray stop ends the recording externally; recordSessions closes Ended
	// and releases the cycle.
	requestStop()
	select {
	case <-sess1.Stop:
	case <-time.After(time.Second):
		t.Fatal("tray stop did not end session")
	}
	close(sess1.Ended)
	isRecording.Store(false)
	time.Sleep(20 * time.Millisecond) // let listenHotkey leave WaitStop

	// 3. The next tap starts a new session at once
	hk.SimKeydown()
	var sess2 recSession
	select {
	case sess2 = <-sessions:
	case <-time.After(time.Second):
		t.Fatal("first tap after a tray stop was swallowed instead of starting a recording")
	}
	time.Sleep(10 * time.Millisecond)
	hk.SimKeyup()

	select {
	case <-sess2.Stop:
		t.Fatal("new session immediately stopped by stale stopCh signal")
	case <-time.After(300 * time.Millisecond):
		// session stayed alive
	}
	isRecording.Store(false)
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

// TestTryStartSessionDeniesDuringCycle pins the shared record-request guard: a
// request while a cycle is active (recording OR still transcribing — both keep
// isRecording set) is denied and enqueues nothing. Both the hotkey and the tray
// "Start Recording" button route through tryStartSession, so this closes the bug
// where a tray click during inference queued an unattended recording.
func TestTryStartSessionDeniesDuringCycle(t *testing.T) {
	isRecording.Store(true)
	defer isRecording.Store(false)

	sessions := make(chan recSession, 1)
	if sc := tryStartSession(sessions); sc != nil {
		t.Fatal("tryStartSession should deny (return nil) while a cycle is active")
	}
	select {
	case <-sessions:
		t.Fatal("no session should be enqueued while a cycle is active")
	default:
	}
}

// TestTryStartSessionEnqueuesWhenIdle verifies the happy path: idle → a session
// is enqueued and its SilenceClose handle returned.
func TestTryStartSessionEnqueuesWhenIdle(t *testing.T) {
	isRecording.Store(false)
	// tryStartSession claims the cycle (isRecording=true) instead of leaving that
	// to recordSessions, which isn't running here — release it like a real cycle
	// end would, so later tests start idle.
	defer isRecording.Store(false)

	sessions := make(chan recSession, 1)
	sc := tryStartSession(sessions)
	if sc == nil {
		t.Fatal("tryStartSession should return a handle when idle")
	}
	select {
	case <-sessions:
	default:
		t.Fatal("tryStartSession should enqueue a session when idle")
	}
}

// TestDeviceChangeAction pins the hotplug decision table — in particular the
// regression where a session started while the preferred mic was unplugged:
// the preference must still win the moment the device is attached.
func TestDeviceChangeAction(t *testing.T) {
	jabra := "Jabra EVOLVE 20 MS"
	cases := []struct {
		name      string
		names     []string
		selected  string
		preferred string
		want      deviceAction
	}{
		{"selected mic unplugged", []string{"MacBook Pro Microphone"}, jabra, jabra, switchToDefault},
		{"preferred plugged in while on default", []string{"MacBook Pro Microphone", jabra}, "", jabra, switchToPreferred},
		{"started without preferred, it appears later", []string{jabra}, "", jabra, switchToPreferred},
		{"on default, no preference", []string{jabra}, "", "", keepDevice},
		{"selected still attached", []string{"MacBook Pro Microphone", jabra}, jabra, jabra, keepDevice},
		{"preferred absent, stay on default", []string{"MacBook Pro Microphone"}, "", jabra, keepDevice},
	}
	for _, c := range cases {
		if got := deviceChangeAction(c.names, c.selected, c.preferred); got != c.want {
			t.Errorf("%s: deviceChangeAction(%v, sel=%q, pref=%q) = %v, want %v",
				c.name, c.names, c.selected, c.preferred, got, c.want)
		}
	}
}

// TestTagColor: a profile gets a palette colour on first use, saved to
// config.json; a colour the user set is kept, unused profiles get none, and
// "none" writes nothing.
func TestTagColor(t *testing.T) {
	config.SetDir(t.TempDir())
	if err := config.Load(); err != nil {
		t.Fatal(err)
	}

	if c := tagColor(""); c != "" {
		t.Errorf("tagColor(\"\") = %q, want none", c)
	}
	if _, err := os.Stat(config.SettingsPath()); err == nil {
		t.Error("config.json written although no profile was used")
	}

	config.Update(func(s *config.Settings) {
		s.PostTranscribe.Colors = map[string]string{"friendly": "#123456"}
	})
	if c := tagColor("friendly"); c != "#123456" {
		t.Errorf("friendly = %q, want the user's #123456 kept", c)
	}
	got := tagColor("clean")
	if !slices.Contains(overlay.TagColors, got) {
		t.Errorf("clean = %q, want a palette colour", got)
	}

	if err := config.Load(); err != nil { // read back what was saved
		t.Fatal(err)
	}
	pt := config.Get().PostTranscribe
	if pt.Colors["clean"] != got || tagColor("clean") != got {
		t.Errorf("clean = %q after reload, want its first colour %q kept", pt.Colors["clean"], got)
	}
	if c, ok := pt.Colors["professional"]; ok {
		t.Errorf("professional = %q, want no colour for an unused profile", c)
	}
}

// TestPasteAtEnd: a streaming session with a profile pastes its held-back text
// even after a session error or a silence auto-close, as the live pastes
// without a profile would have; batch and live-stream behaviour is unchanged.
func TestPasteAtEnd(t *testing.T) {
	errSess := errors.New("stream closed")
	batch := recordingConfig{}
	batchPost := recordingConfig{post: config.PostTranscribe{Active: "clean"}}
	live := recordingConfig{stream: true}
	held := recordingConfig{stream: true, post: config.PostTranscribe{Active: "clean"}}
	for _, c := range []struct {
		name       string
		cfg        recordingConfig
		hasText    bool
		err        error
		autoClosed bool
		want       bool
	}{
		{"batch", batch, true, nil, false, true},
		{"batch with profile", batchPost, true, nil, false, true},
		{"batch error", batch, true, errSess, false, false},
		{"batch with profile error", batchPost, true, errSess, false, false},
		{"batch auto-closed", batch, true, nil, true, false},
		{"batch no text", batch, false, nil, false, false},
		{"live stream", live, true, nil, false, false},
		{"live stream error", live, true, errSess, false, false},
		{"held stream", held, true, nil, false, true},
		{"held stream error", held, true, errSess, false, true},
		{"held stream auto-closed", held, true, nil, true, true},
		{"held stream no text", held, false, errSess, false, false},
	} {
		if got := pasteAtEnd(c.cfg, c.hasText, c.err, c.autoClosed); got != c.want {
			t.Errorf("%s: pasteAtEnd = %v, want %v", c.name, got, c.want)
		}
	}
}

// errSession is a finished session that returns committed text together with
// a session error, as streamSession.Close does when the stream fails midway.
type errSession struct{ updates chan string }

func (errSession) Feed([]byte)              {}
func (s errSession) Updates() <-chan string { return s.updates }
func (errSession) Close() (transcriber.SessionResult, error) {
	return transcriber.SessionResult{Text: "git rebase", HasText: true}, errors.New("stream closed")
}

// TestFinishTranscriptionHeldBackError: a held-back stream that fails midway
// keeps its raw text as the last text; without a profile it was pasted live
// and the error path leaves the last text alone, as before.
func TestFinishTranscriptionHeldBackError(t *testing.T) {
	for _, c := range []struct {
		name    string
		profile string
		want    string
	}{
		{"profile", "clean", "git rebase"},
		{"no profile", "", ""},
	} {
		clip.SetLastText("")
		done := make(chan struct{})
		close(done)
		sess := errSession{updates: make(chan string)}
		close(sess.updates)
		cfg := recordingConfig{stream: true, post: config.PostTranscribe{Active: c.profile}}
		finishTranscription(sess, nil, done, false, time.Second, cfg)
		clip.textMu.Lock()
		got := clip.lastText
		clip.textMu.Unlock()
		if got != c.want {
			t.Errorf("%s: last text = %q, want %q", c.name, got, c.want)
		}
	}
}
