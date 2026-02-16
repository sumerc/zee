package main

import "testing"

func pttMonitor() *silenceMonitor {
	return newSilenceMonitor(func() bool { return false })
}

func toggleMonitor() *silenceMonitor {
	return newSilenceMonitor(func() bool { return true })
}

func feedN(m *silenceMonitor, speech bool, n int) SilenceEvent {
	var last SilenceEvent
	for i := 0; i < n; i++ {
		last = m.Tick(speech)
	}
	return last
}

func TestSilenceWarnAfter8s(t *testing.T) {
	m := pttMonitor()
	// 79 ticks of silence — no warning yet
	for i := 0; i < 79; i++ {
		if ev := m.Tick(false); ev != SilenceNone {
			t.Fatalf("unexpected event at tick %d: %d", i, ev)
		}
	}
	// 80th tick triggers warning (8s)
	if ev := m.Tick(false); ev != SilenceWarn {
		t.Fatalf("expected SilenceWarn at tick 80, got %d", ev)
	}
}

func TestSilenceWarnClearsOnSpeech(t *testing.T) {
	m := pttMonitor()
	feedN(m, false, 80) // triggers warn

	// Sustained speech clears warning (need 25% of 80-tick window)
	for i := 0; i < 80; i++ {
		ev := m.Tick(true)
		if ev == SilenceWarnClear {
			return
		}
	}
	t.Fatal("expected SilenceWarnClear after speech")
}

func TestNoWarnDuringSpeech(t *testing.T) {
	m := pttMonitor()
	for i := 0; i < 200; i++ {
		if ev := m.Tick(true); ev == SilenceWarn {
			t.Fatalf("unexpected warn during speech at tick %d", i)
		}
	}
}

func TestToggleRepeatBeep(t *testing.T) {
	m := toggleMonitor()
	feedN(m, false, 80) // warn at tick 80
	// Next repeat at tick 80 + 80 = 160
	var gotRepeat bool
	for i := 0; i < 100; i++ {
		if ev := m.Tick(false); ev == SilenceRepeat {
			gotRepeat = true
			break
		}
	}
	if !gotRepeat {
		t.Fatal("expected SilenceRepeat in toggle mode")
	}
}

func TestAutoClosePriorityOverRepeat(t *testing.T) {
	m := toggleMonitor()
	for i := 0; i < 400; i++ {
		ev := m.Tick(false)
		if ev == SilenceAutoClose {
			return
		}
		if i >= 300 && ev == SilenceRepeat {
			t.Fatalf("SilenceRepeat fired at tick %d instead of SilenceAutoClose", i)
		}
	}
	t.Fatal("expected SilenceAutoClose within 400 ticks")
}

func TestToggleAutoClose(t *testing.T) {
	m := toggleMonitor()
	var gotClose bool
	for i := 0; i < 400; i++ {
		if ev := m.Tick(false); ev == SilenceAutoClose {
			gotClose = true
			break
		}
	}
	if !gotClose {
		t.Fatal("expected SilenceAutoClose after 300 ticks")
	}
}

func TestNoAutoCloseInPTT(t *testing.T) {
	m := pttMonitor()
	for i := 0; i < 400; i++ {
		if ev := m.Tick(false); ev == SilenceAutoClose {
			t.Fatalf("unexpected auto-close in PTT mode at tick %d", i)
		}
	}
}

func TestAutoClosePreventedBySpeech(t *testing.T) {
	m := toggleMonitor()
	for i := 0; i < 500; i++ {
		speech := i%10 < 7
		if ev := m.Tick(speech); ev == SilenceAutoClose {
			t.Fatalf("unexpected auto-close with speech at tick %d", i)
		}
	}
}

func TestNoRepeatInPTT(t *testing.T) {
	m := pttMonitor()
	for i := 0; i < 300; i++ {
		if ev := m.Tick(false); ev == SilenceRepeat {
			t.Fatalf("unexpected SilenceRepeat in PTT mode at tick %d", i)
		}
	}
}

func TestWarnOnlyOnce(t *testing.T) {
	m := pttMonitor()
	warns := 0
	for i := 0; i < 300; i++ {
		if ev := m.Tick(false); ev == SilenceWarn {
			warns++
		}
	}
	if warns != 1 {
		t.Fatalf("expected exactly 1 SilenceWarn in PTT mode, got %d", warns)
	}
}

func TestWarnStaysDuringNoise(t *testing.T) {
	m := pttMonitor()
	feedN(m, false, 80) // triggers warn

	// Occasional VAD false positives (< 25% speech) should NOT clear
	clears := 0
	for i := 0; i < 80; i++ {
		speech := i%10 == 0 // 10% speech — below clear threshold
		if ev := m.Tick(speech); ev == SilenceWarnClear {
			clears++
		}
	}
	if clears > 0 {
		t.Fatalf("expected warning to stay with 10%% speech, got %d clears", clears)
	}
}
