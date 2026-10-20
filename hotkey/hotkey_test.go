package hotkey

import (
	"errors"
	"testing"
	"time"
)

func TestDefaultComboValid(t *testing.T) {
	c := DefaultCombo()
	if c.IsZero() {
		t.Fatal("DefaultCombo must not be zero")
	}
	if !hasModifier(c.Mods) {
		t.Fatal("DefaultCombo must have at least one modifier")
	}
	if c.Label == "" {
		t.Fatal("DefaultCombo must have a display label")
	}
}

func TestComboIsZero(t *testing.T) {
	if !(Combo{}).IsZero() {
		t.Fatal("empty Combo should be zero")
	}
	if (Combo{Mods: []string{"ctrl"}, Key: 49}).IsZero() {
		t.Fatal("populated Combo should not be zero")
	}
}

func TestFakeRebindAndCapture(t *testing.T) {
	hk := NewFake()

	// Rebind requires a modifier.
	if err := hk.Rebind(Combo{Key: 49}); err == nil {
		t.Fatal("rebind without a modifier should fail")
	}
	want := Combo{Mods: []string{"cmd", "shift"}, Key: 49, Label: "⌘⇧Space"}
	if err := hk.Rebind(want); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if hk.Current().Label != want.Label {
		t.Fatalf("Current() = %q, want %q", hk.Current().Label, want.Label)
	}

	// Capture returns canceled when nothing is scripted, the chord otherwise.
	if _, err := hk.Capture(nil); !errors.Is(err, ErrCaptureCanceled) {
		t.Fatalf("empty capture should be canceled, got %v", err)
	}
	hk.NextCapture = want
	got, err := hk.Capture(nil)
	if err != nil || got.Label != want.Label {
		t.Fatalf("Capture = (%v, %v), want (%q, nil)", got, err, want.Label)
	}
}

func TestComboEqual(t *testing.T) {
	a := Combo{Mods: []string{"ctrl", "shift"}, Key: 49, Label: "⌃⇧Space"}
	b := Combo{Mods: []string{"shift", "ctrl"}, Key: 49, Label: "whatever"} // order + label ignored
	if !a.Equal(b) {
		t.Fatal("same chord with reordered mods should be equal")
	}
	if a.Equal(Combo{Mods: []string{"ctrl", "shift"}, Key: 50}) {
		t.Fatal("different key should not be equal")
	}
	if a.Equal(Combo{Mods: []string{"ctrl"}, Key: 49}) {
		t.Fatal("different mods should not be equal")
	}
}

// TestWaitStopTogglePress: in toggle mode the next tap stops the recording.
func TestWaitStopTogglePress(t *testing.T) {
	hk := NewFake()
	done := make(chan bool, 1)
	go func() {
		_, toggled := WaitStop(hk, time.Hour, nil, nil)
		done <- toggled
	}()
	hk.SimKeyup() // short tap → toggle mode
	hk.SimKeydown()
	hk.SimKeyup()
	select {
	case toggled := <-done:
		if !toggled {
			t.Fatal("a short tap should resolve to toggle mode")
		}
	case <-time.After(time.Second):
		t.Fatal("WaitStop did not return on the stop tap")
	}
}

// TestWaitStopToggleEndedElsewhere: a toggle recording that ends another way
// (silence auto-close, tray stop, error) must release WaitStop without
// consuming a key event — the next press belongs to a new recording.
func TestWaitStopToggleEndedElsewhere(t *testing.T) {
	hk := NewFake()
	ended := make(chan struct{})
	done := make(chan struct{})
	go func() {
		WaitStop(hk, time.Hour, ended, nil)
		close(done)
	}()
	hk.SimKeyup() // short tap → toggle mode
	close(ended)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WaitStop still waiting for a stop tap after the recording ended")
	}
	hk.SimKeydown()
	if len(hk.Keydown()) != 1 {
		t.Fatal("the next press was consumed by the finished recording")
	}
}
