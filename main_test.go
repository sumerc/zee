package main

import (
	"testing"
	"time"
	"zee/hotkey"
)

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
