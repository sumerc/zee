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
	hk.SimKeydown()
	sess1 := <-sessions
	time.Sleep(10 * time.Millisecond)
	hk.SimKeyup()

	// 2. Tray stop ends the recording externally
	fireTrayStop()
	select {
	case <-sess1.Stop:
	case <-time.After(time.Second):
		t.Fatal("tray stop did not end session")
	}

	// 3. Still in toggleRecording — this tap transitions back to idle
	//    and sends a (now stale) stop signal to stopCh
	hk.SimKeydown()
	hk.SimKeyup()
	time.Sleep(20 * time.Millisecond) // let state machine settle

	// 4. New tap should start a session that stays alive
	hk.SimKeydown()
	sess2 := <-sessions
	time.Sleep(10 * time.Millisecond)
	hk.SimKeyup()

	select {
	case <-sess2.Stop:
		t.Fatal("new session immediately stopped by stale stopCh signal")
	case <-time.After(300 * time.Millisecond):
		// session stayed alive — fix works
	}
}
