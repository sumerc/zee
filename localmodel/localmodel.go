// Package localmodel is the single source of truth for the offline Parakeet
// GGUF models: their filenames, download URLs, checksums, sizes, and decoder
// head. It resolves where models live on disk and downloads missing ones
// atomically (tmp → verify sha256 → rename).
//
// Decoder values match internal/parakeet.Decoder* (0=default, 1=ctc, 2=tdt).
// Keeping them here as plain ints keeps this package free of the cgo engine so
// it stays cross-platform (the tray and installer reference it everywhere).
package localmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"zee/config"
)

// Version is the pinned model-set version. It drives BOTH the download tag and
// the dev folder, so they never drift (decision #5: models are pinned to the
// binary, never "latest"). Bump it when the parakeet.cpp commit changes.
const Version = "v1"

// baseURL hosts the immutable models-<Version> release assets.
const baseURL = "https://github.com/sumerc/zee/releases/download/models-" + Version + "/"

// Model IDs (stable; persisted in config.json and shown in the tray).
const (
	ID110mEN  = "parakeet-110m-en"     // default, English-only, loaded at startup
	IDV3Multi = "parakeet-v3-multi"    // multilingual (25 lang), pre-fetched
	IDV2Large = "parakeet-v2-en-large" // English long-form, opt-in download
)

// Model is one downloadable GGUF plus everything zee needs to load, route to,
// and verify it.
type Model struct {
	ID           string
	Label        string
	Filename     string
	SHA256       string
	SizeBytes    int64
	Decoder      int  // parakeet head: 0=default, 1=ctc, 2=tdt
	Multilingual bool // true => non-English supported (v3); false => English-only
	PreFetch     bool // install.sh pre-fetches it (110m + v3); v2 never
}

// URL is where the gguf is hosted under the pinned models tag.
func (m Model) URL() string { return baseURL + m.Filename }

// models is ordered: default first, then the pre-fetched multilingual option,
// then the opt-in large English model.
var models = []Model{
	{
		ID:        ID110mEN,
		Label:     "Parakeet 110M (English)",
		Filename:  "tdt_ctc-110m-f16.gguf",
		SHA256:    "7f9a6376edde6a74592ace48b2ebdc27a1ac972d0be9dfcc29e668d99381faf1",
		SizeBytes: 267452544,
		Decoder:   2, // TDT head
		PreFetch:  true,
	},
	{
		ID:           IDV3Multi,
		Label:        "Parakeet 0.6B v3 (multilingual)",
		Filename:     "tdt-0.6b-v3-q4_k.gguf",
		SHA256:       "993d73feb4206dadda865ab25bd64b50c48dc4d013c3bf6126a721f28b1d5ee8",
		SizeBytes:    675200864,
		Decoder:      0, // default head
		Multilingual: true,
		PreFetch:     true,
	},
	{
		ID:        IDV2Large,
		Label:     "Parakeet 0.6B v2 (English, large)",
		Filename:  "tdt-0.6b-v2-f16.gguf",
		SHA256:    "f8df7f5dc7b9ceb5cd0637a81194aab5d93022ace555ce81c8969c7a694b8f3d",
		SizeBytes: 1404218656,
		Decoder:   0, // default head
		PreFetch:  false,
	},
}

// All returns the registry in display order.
func All() []Model { return models }

// ByID looks up a model by its stable ID.
func ByID(id string) (Model, bool) {
	for _, m := range models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// Default returns the model loaded at startup (110m English).
func Default() Model { m, _ := ByID(ID110mEN); return m }

// Dir is where ggufs live: $ZEE_MODELS_DIR override, else <config dir>/models
// for the installed app (the install/download location), else the versioned dev
// folder ./models/parakeet/<Version>. The app-vs-dev split uses the same
// executable-path signal as the login item (config.IsAppBundle), so prod is
// detected reliably regardless of working directory.
func Dir() string {
	if d := os.Getenv("ZEE_MODELS_DIR"); d != "" {
		return d
	}
	if config.IsAppBundle() {
		return filepath.Join(config.Dir(), "models")
	}
	return filepath.Join("models", "parakeet", Version)
}

// Path is the on-disk location of a model's gguf (whether or not it exists).
func Path(m Model) string { return filepath.Join(Dir(), m.Filename) }

// Present reports whether the model's gguf exists on disk at the right size.
// (A size check is cheap and catches truncated/aborted downloads; the full
// sha256 is verified at download time, not on every startup.)
func Present(m Model) bool {
	fi, err := os.Stat(Path(m))
	return err == nil && fi.Size() == m.SizeBytes
}

// Download fetches a model to Dir() atomically: stream to a temp file, verify
// the sha256, then rename into place. progress (may be nil) is called with the
// fraction downloaded in [0,1]. A no-op if the model is already present.
func Download(m Model, progress func(fraction float64)) error {
	if Present(m) {
		return nil
	}
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create models dir: %w", err)
	}
	if err := checkDiskSpace(dir, m.SizeBytes); err != nil {
		return err
	}

	resp, err := http.Get(m.URL())
	if err != nil {
		return fmt.Errorf("download %s: %w", m.Filename, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", m.Filename, resp.StatusCode)
	}

	tmp, err := os.CreateTemp(dir, "."+m.Filename+".*.part")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op after a successful rename

	h := sha256.New()
	pr := &progressReader{r: resp.Body, total: m.SizeBytes, cb: progress}
	if _, err := io.Copy(io.MultiWriter(tmp, h), pr); err != nil {
		tmp.Close()
		return fmt.Errorf("download %s: %w", m.Filename, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}

	if got := hex.EncodeToString(h.Sum(nil)); got != m.SHA256 {
		return fmt.Errorf("checksum mismatch for %s (got %s, want %s)", m.Filename, got, m.SHA256)
	}

	if err := os.Rename(tmpPath, Path(m)); err != nil {
		return fmt.Errorf("install %s: %w", m.Filename, err)
	}
	return nil
}

// progressReader reports download progress at most ~10x/sec via cb.
type progressReader struct {
	r      io.Reader
	total  int64
	read   int64
	cb     func(float64)
	lastAt time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if p.cb != nil && p.total > 0 {
		now := time.Now()
		if err != nil || now.Sub(p.lastAt) > 100*time.Millisecond {
			p.lastAt = now
			frac := float64(p.read) / float64(p.total)
			if frac > 1 {
				frac = 1
			}
			p.cb(frac)
		}
	}
	return n, err
}
