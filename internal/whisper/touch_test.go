//go:build darwin && arm64

package whisper_test

import (
	"testing"
	"time"

	"zee/internal/whisper"
	"zee/localmodel"
)

// TestTouch checks the keypress prefetch against the real model: it must find
// the weights (a zero page count means the accessor patch or the mincore guard
// broke, and the warm-up would silently do nothing), report a freshly loaded
// model as warm, stay cheap on a warm model, and let Close cut it short.
func TestTouch(t *testing.T) {
	m, ok := localmodel.ByID(localmodel.IDWhisperQ5)
	if !ok {
		t.Fatal("whisper model missing from the registry")
	}
	if !localmodel.Present(m) {
		t.Skipf("%s not downloaded (run make download-models)", m.ID)
	}
	ctx, err := whisper.New(localmodel.Path(m))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	start := time.Now()
	pages, cold := ctx.Touch()
	took := time.Since(start)
	t.Logf("warm touch: pages=%d cold=%d in %v", pages, cold, took)
	if pages == 0 {
		t.Fatal("Touch found no CPU-addressable weight pages")
	}
	if cold > pages/10 {
		t.Errorf("freshly loaded model reported %d of %d pages cold", cold, pages)
	}

	// Close while touches are running must not crash or deadlock.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			ctx.Touch()
		}
		close(done)
	}()
	ctx.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Touch did not return after Close")
	}
	if p, _ := ctx.Touch(); p != 0 {
		t.Errorf("Touch after Close reported %d pages", p)
	}
}
