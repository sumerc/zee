package main

// Auto-correct: an optional post-transcription pass through Superwhisper's
// S1-mini (internal/s1) that turns raw ASR output into clean written text —
// fillers dropped, punctuation and casing applied, spoken numbers/dates written
// out. "Auto-correct" is the user-facing name (config.json `auto_correct`, tray
// checkbox, -autocorrect flag); the model itself is a text normalizer.
// English-only, so callers gate on the session language; on any failure the raw
// transcript is used unchanged.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"zee/internal/s1"
	"zee/localmodel"
	"zee/log"
	"zee/transcriber"
)

// s1ModelFile lives next to the STT ggufs (localmodel.Dir()) but is not in the
// localmodel registry: it is a text cleaner, not an STT model, and this is an
// experiment — drop the file in place to enable it.
const s1ModelFile = "s1-mini-q4_k_m.gguf"

var (
	s1Mu      sync.Mutex
	s1Ctx     *s1.Ctx // nil until a load finishes
	s1Loading bool    // a load goroutine is in flight
	// s1Unavailable means auto-correct can never come up this session (platform
	// stub or no model file). It separates "skip silently forever" from
	// "loading/failed", so the per-dictation log lines below appear only when
	// the user expects auto-correct to be working.
	s1Unavailable bool
)

// autoCorrect is the runtime toggle (flag/config at startup, tray checkbox
// after). Guarded by configMu like autoPaste. The model stays loaded when
// toggled off, so re-enabling from the tray is instant.
var autoCorrect bool

// loadAutoCorrect starts loading S1-mini in the background and returns a
// channel closed when the load settles (either way). Idempotent: extra calls
// (tray re-enable) return an already-closed channel once a load ran. The tray
// app ignores the channel — a dictation that beats the load just skips the pass
// — while the file-driven modes (-test, -transcribe) wait on it so their output
// is deterministic.
func loadAutoCorrect() <-chan struct{} {
	done := make(chan struct{})
	path := filepath.Join(localmodel.Dir(), s1ModelFile)

	s1Mu.Lock()
	if s1Ctx != nil || s1Loading || s1Unavailable {
		s1Mu.Unlock()
		close(done)
		return done
	}
	if !s1.Available() {
		s1Unavailable = true
		s1Mu.Unlock()
		close(done)
		return done
	}
	if _, err := os.Stat(path); err != nil {
		s1Unavailable = true
		s1Mu.Unlock()
		log.Info("autocorrect: disabled, no model at " + path)
		close(done)
		return done
	}
	s1Loading = true
	s1Mu.Unlock()

	go func() {
		defer close(done)
		t := time.Now()
		c, err := s1.New(path)
		s1Mu.Lock()
		s1Ctx = c
		s1Loading = false
		s1Mu.Unlock()
		if err != nil {
			log.Warnf("autocorrect: %v", err)
			return
		}
		log.Info(fmt.Sprintf("autocorrect: s1-mini loaded (%d ms incl. warm-up)", time.Since(t).Milliseconds()))
	}()
	return done
}

// autoCorrectEnglish reports whether the transcript is definitely English, the
// only language S1-mini handles: either the session language is explicitly
// "en", or the active model cannot produce anything else. Auto-detect on a
// multilingual model does not qualify.
func autoCorrectEnglish(tr transcriber.Transcriber) bool {
	if tr.GetLanguage() == "en" {
		return true
	}
	langs := tr.SupportedLanguages()
	return len(langs) == 1 && langs[0].Code == "en"
}

// maybeAutoCorrect is the per-dictation entry point: it applies S1-mini when
// the toggle is on and the transcript is definitely English, and returns the
// input unchanged otherwise — the transcript must never be lost to cleanup.
// Every call emits exactly one `autocorrect applied=true|false` diagnostic line
// (unless the feature is off or unavailable for the whole session), so the log
// answers "did it run?" per dictation. reason=not_english means the language
// gate blocked it: set the language to English in the tray (or use an
// English-only model) to enable.
func maybeAutoCorrect(tr transcriber.Transcriber, text string) string {
	configMu.Lock()
	on := autoCorrect
	configMu.Unlock()
	s1Mu.Lock()
	c, unavailable := s1Ctx, s1Unavailable
	s1Mu.Unlock()
	if !on || unavailable {
		return text
	}
	if !autoCorrectEnglish(tr) {
		log.Info("autocorrect applied=false reason=not_english")
		return text
	}
	if c == nil {
		log.Info("autocorrect applied=false reason=model_loading")
		return text
	}
	if strings.TrimSpace(text) == "" {
		return text
	}
	t := time.Now()
	clean, err := c.Normalize(text)
	if err != nil {
		log.Warnf("autocorrect applied=false reason=error err=%v", err)
		return text
	}
	log.Info(fmt.Sprintf("autocorrect applied=true ms=%d", time.Since(t).Milliseconds()))
	return clean
}
