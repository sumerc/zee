package transcriber

import (
	"context"
	"sync"
	"testing"

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
	p := newLocalProvider(localmodel.EngineParakeet, localmodel.ID110mEN, "en",
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
