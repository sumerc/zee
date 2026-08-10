package transcriber

import (
	"sync/atomic"
	"testing"
	"time"
)

type warmStubEngine struct{ calls atomic.Int32 }

func (e *warmStubEngine) Transcribe([]float32, string, string) (string, error) {
	e.calls.Add(1)
	return "", nil
}
func (e *warmStubEngine) Close() {}

// Warm must run a touch inference only when the engine is loaded AND idle past
// the threshold. The recently-used and not-ready cases must be no-ops: warming
// on every keydown would burn GPU for nothing, and warming a half-loaded
// provider would race the loader.
func TestWarmIdleGate(t *testing.T) {
	eng := &warmStubEngine{}
	p := &localProvider{name: "stub", modelID: "m", loadedID: "m", engine: eng}

	p.lastUsed = time.Now()
	p.Warm()
	if n := eng.calls.Load(); n != 0 {
		t.Fatalf("recently used: want no warm inference, got %d", n)
	}

	p.lastUsed = time.Now().Add(-warmIdleThreshold - time.Minute)
	p.Warm()
	if n := eng.calls.Load(); n != 1 {
		t.Fatalf("idle past threshold: want 1 warm inference, got %d", n)
	}
	if time.Since(p.lastUsed) > time.Second {
		t.Fatal("Warm did not refresh lastUsed — the next keydown would warm again")
	}

	// Mid-switch (loadedID != modelID) and unloaded engines must be skipped.
	p.lastUsed = time.Now().Add(-warmIdleThreshold - time.Minute)
	p.modelID = "other"
	p.Warm()
	p.modelID, p.engine = "m", nil
	p.Warm()
	if n := eng.calls.Load(); n != 1 {
		t.Fatalf("not-ready provider: want no extra inference, got %d", n)
	}
}
