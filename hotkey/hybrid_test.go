package hotkey

import (
	"testing"
	"time"
)

func waitStart(t *testing.T, hy *Hybrid) {
	t.Helper()
	select {
	case <-hy.Start():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for start")
	}
}

func waitStop(t *testing.T, hy *Hybrid) {
	t.Helper()
	select {
	case <-hy.StopChan():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stop")
	}
}

func TestHybridLongPress(t *testing.T) {
	fk := NewFake()
	threshold := 50 * time.Millisecond
	hy := NewHybrid(fk, threshold)

	fk.SimKeydown()
	waitStart(t, hy)

	time.Sleep(threshold + 20*time.Millisecond)
	if hy.IsToggle() {
		t.Error("expected PTT (not toggle) after long press")
	}
	fk.SimKeyup()
	waitStop(t, hy)
}

func TestHybridShortTap(t *testing.T) {
	fk := NewFake()
	threshold := 200 * time.Millisecond
	hy := NewHybrid(fk, threshold)

	fk.SimKeydown()
	waitStart(t, hy)
	fk.SimKeyup() // release before threshold → toggle mode
	time.Sleep(10 * time.Millisecond)
	if !hy.IsToggle() {
		t.Error("expected toggle mode after short tap")
	}

	// Should NOT have stopped yet
	select {
	case <-hy.StopChan():
		t.Fatal("unexpected stop after short tap — should still be recording")
	case <-time.After(50 * time.Millisecond):
	}

	// Second press+release stops toggle recording
	fk.SimKeydown()
	fk.SimKeyup()
	waitStop(t, hy)
}

func TestHybridMultipleCycles(t *testing.T) {
	fk := NewFake()
	threshold := 50 * time.Millisecond
	hy := NewHybrid(fk, threshold)

	// Cycle 1: long press (PTT)
	fk.SimKeydown()
	waitStart(t, hy)
	time.Sleep(threshold + 20*time.Millisecond)
	fk.SimKeyup()
	waitStop(t, hy)

	// Cycle 2: short tap (toggle)
	fk.SimKeydown()
	waitStart(t, hy)
	fk.SimKeyup()
	time.Sleep(20 * time.Millisecond) // let state machine settle
	fk.SimKeydown()
	fk.SimKeyup()
	waitStop(t, hy)

	// Cycle 3: long press again
	fk.SimKeydown()
	waitStart(t, hy)
	time.Sleep(threshold + 20*time.Millisecond)
	fk.SimKeyup()
	waitStop(t, hy)
}
