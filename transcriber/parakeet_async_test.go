package transcriber

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"zee/localmodel"
)

// TestLocalProviderConcurrentAccess stresses the async-load locking design:
// newLocalProvider warms the default model in a background goroutine, and this
// hammers the metadata accessors, model swaps, and NewSession concurrently.
// Under -race it proves the two-lock split (mu for fast fields, loadMu
// serializing load) has no data race or deadlock, and that metadata reads never
// block on an in-flight load. It doesn't assert on transcripts — with or without
// a model on disk the lock paths are the same (a missing model just surfaces as
// a NewSession error).
func TestLocalProviderConcurrentAccess(t *testing.T) {
	p := newLocalProvider(localmodel.EngineParakeet, localmodel.ID110mEN, "en", false,
		openParakeet, parakeetLanguages)
	defer p.Close()

	ids := []string{localmodel.ID110mEN, localmodel.IDWhisperQ5, "does-not-exist"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				switch (n + j) % 5 {
				case 0:
					p.SetModel(ids[(n+j)%len(ids)])
				case 1:
					_ = p.GetModel()
				case 2:
					p.SetLanguage("en")
				case 3:
					_ = p.SupportedLanguages()
				case 4:
					if s, err := p.NewSession(context.Background(), SessionConfig{}); err == nil {
						_ = s.Close
					}
				}
			}
		}(i)
	}
	wg.Wait()
}

// countEngine "transcribes" by reporting how many samples it was given.
type countEngine struct{}

func (countEngine) Transcribe(pcm []float32, _, _ string) (string, error) {
	return fmt.Sprintf("%d samples", len(pcm)), nil
}

func (countEngine) Close() {}

// TestLocalSessionStartsBeforeLoad: capture starts on the keypress, so
// NewSession must not wait for a model that is still loading — speech spoken
// meanwhile used to be lost while the start beep already said "recording".
// The audio buffered during the load must be transcribed once it finishes.
func TestLocalSessionStartsBeforeLoad(t *testing.T) {
	m, _ := localmodel.ByID(localmodel.ID110mEN)
	dir := t.TempDir()
	t.Setenv("ZEE_MODELS_DIR", dir)
	f, err := os.Create(filepath.Join(dir, m.Filename))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(m.SizeBytes); err != nil { // sparse: Present only checks the size
		t.Fatal(err)
	}
	f.Close()

	release := make(chan struct{})
	slowOpen := func(localmodel.Model) (localEngine, error) {
		<-release
		return countEngine{}, nil
	}
	p := newLocalProvider(localmodel.EngineParakeet, m.ID, "en", false, slowOpen, parakeetLanguages)
	defer p.Close()

	sessCh := make(chan Session, 1)
	go func() {
		s, err := p.NewSession(context.Background(), SessionConfig{})
		if err != nil {
			t.Errorf("NewSession: %v", err)
		}
		sessCh <- s
	}()
	var sess Session
	select {
	case sess = <-sessCh:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("NewSession blocked on the model load")
	}
	if sess == nil {
		close(release)
		return
	}

	pcm := make([]byte, 3200) // 1600 samples
	sess.Feed(pcm)

	resCh := make(chan SessionResult, 1)
	go func() {
		res, err := sess.Close()
		if err != nil {
			t.Errorf("Close: %v", err)
		}
		resCh <- res
	}()
	select {
	case <-resCh:
		t.Fatal("Close returned before the model finished loading")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case res := <-resCh:
		if res.Text != "1600 samples" {
			t.Fatalf("Text = %q, want the buffered audio transcribed (%q)", res.Text, "1600 samples")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the load finished")
	}
}
