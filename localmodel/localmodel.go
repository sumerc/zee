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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"zee/config"
)

// Version is the pinned model-set version. It drives BOTH the download tag and
// the dev folder, so they never drift (decision #5: models are pinned to the
// binary, never "latest"). Bump it when the parakeet.cpp commit changes.
const Version = "v2"

// baseURL hosts the immutable models-<Version> release assets.
const baseURL = "https://github.com/sumerc/zee/releases/download/models-" + Version + "/"

// Engine names the local backend that loads a model. It picks the cgo wrapper
// (internal/parakeet vs internal/whisper) and, with it, the model's language
// behaviour — parakeet has no language parameter at all, whisper takes one.
const (
	EngineParakeet = "parakeet"
	EngineWhisper  = "whisper"
)

// Model IDs (stable; persisted in config.json and shown in the tray).
const (
	ID110mEN    = "parakeet-110m-en"  // default, English-only, loaded at startup
	IDWhisperQ5 = "whisper-turbo-q5"  // multilingual, pre-fetched
	IDV3Multi   = "parakeet-v3-multi" // multilingual fast path, opt-in download
)

// retiredIDs maps model IDs that no longer exist to their successor, so a
// config.json written by an older build still resolves instead of erroring on
// the user's first recording. The successor keeps the same role — a
// multilingual user must not be silently downgraded to an English-only model.
// (v3-multi was retired in models-v2, then un-retired: on Metal it transcribes
// dictation-length clips 8–17× faster than Whisper, which matters most on
// M1-class machines. A config that migrated to Whisper meanwhile keeps Whisper.)
var retiredIDs = map[string]string{
	"parakeet-v2-en-large": ID110mEN, // English slot, retired in models-v2
}

// Model is one downloadable model file plus everything zee needs to load, route
// to, and verify it.
type Model struct {
	ID           string
	Label        string
	Engine       string // EngineParakeet | EngineWhisper
	Filename     string
	SHA256       string
	SizeBytes    int64
	Decoder      int  // parakeet head: 0=default, 1=ctc, 2=tdt (ignored by whisper)
	Multilingual bool // true => non-English supported; false => English-only
	PreFetch     bool // install.sh pre-fetches it (currently every model)
}

// URL is where the gguf is hosted under the pinned models tag.
func (m Model) URL() string { return baseURL + m.Filename }

// HumanSize renders the on-disk size as "1.4 GB" or "267 MB".
func (m Model) HumanSize() string {
	if m.SizeBytes >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(m.SizeBytes)/(1<<30))
	}
	return fmt.Sprintf("%d MB", m.SizeBytes>>20)
}

// models is ordered fastest-first, which is also the tray's display order.
// Labels are role-based (what the model is *for*), not model names — users
// pick "English, fastest" or "Multilingual", not a parakeet variant; the model
// name rides along in parentheses for the few who know it. Three models, three
// roles: Parakeet 110m is the instant English default, Parakeet v3 the fast
// multilingual option (25 languages, opt-in download), Whisper the coverage
// engine (~99 languages, and English when accuracy beats latency).
//
// Quantization is deliberately absent from labels: v3 is q4_k (identical WER
// to f32 on the parity clip, see parakeet.cpp docs/quantization.md), Whisper
// q5_0 (measured a wash against f16 on latency and quality, ~1 GB smaller).
var models = []Model{
	{
		ID:        ID110mEN,
		Label:     "English — fastest (Parakeet 110M)",
		Engine:    EngineParakeet,
		Filename:  "tdt_ctc-110m-f16.gguf",
		SHA256:    "7f9a6376edde6a74592ace48b2ebdc27a1ac972d0be9dfcc29e668d99381faf1",
		SizeBytes: 267452544,
		Decoder:   2, // TDT head
		PreFetch:  true,
	},
	{
		ID:           IDV3Multi,
		Label:        "Multilingual — fast (Parakeet v3, 25 languages)",
		Engine:       EngineParakeet,
		Filename:     "tdt-0.6b-v3-q4_k.gguf",
		SHA256:       "993d73feb4206dadda865ab25bd64b50c48dc4d013c3bf6126a721f28b1d5ee8",
		SizeBytes:    675200864,
		Decoder:      0, // default head
		Multilingual: true,
	},
	{
		ID:           IDWhisperQ5,
		Label:        "Multilingual — most languages (Whisper, 99)",
		Engine:       EngineWhisper,
		Filename:     "ggml-large-v3-turbo-q5_0.bin",
		SHA256:       "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2",
		SizeBytes:    574041195,
		Multilingual: true,
		PreFetch:     true,
	},
}

// All returns the registry in display order.
func All() []Model { return models }

// Manifest renders the registry as the flat, bash-parseable text install.sh
// consumes: one model per line, `filename<TAB>sha256<TAB>prefetch`. It is the
// single serialization of the registry for non-Go consumers. localmodel/
// manifest.txt is this output committed to the repo (regenerated by
// `make manifest`); TestManifestUpToDate fails if the two drift.
func Manifest() string {
	var b strings.Builder
	b.WriteString("# zee local model manifest — generated from localmodel.go (make manifest); do not edit by hand\n")
	b.WriteString("# filename\tsha256\tprefetch\n")
	for _, m := range models {
		fmt.Fprintf(&b, "%s\t%s\t%t\n", m.Filename, m.SHA256, m.PreFetch)
	}
	return b.String()
}

// ByID looks up a model by its stable ID, following retiredIDs first so a saved
// config from an older build still resolves.
func ByID(id string) (Model, bool) {
	if successor, ok := retiredIDs[id]; ok {
		id = successor
	}
	for _, m := range models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// Default returns the model loaded at startup (110m English).
func Default() Model { m, _ := ByID(ID110mEN); return m }

// Dir is where ggufs live, in priority order:
//   - $ZEE_MODELS_DIR override (set by `make download-models` and the tests);
//   - dev builds: the versioned folder next to the binary,
//     <exe dir>/models/local/<Version>, when it exists (populated by
//     `make download-models`) — resolved against the executable, not the cwd, so
//     `./zee` finds it from any working directory;
//   - otherwise the stable per-user <config dir>/models (the .app bundle and
//     tar.gz installs, which have no in-repo folder).
func Dir() string {
	if d := os.Getenv("ZEE_MODELS_DIR"); d != "" {
		return d
	}
	if !config.IsAppBundle() {
		if exe, err := os.Executable(); err == nil {
			dev := filepath.Join(filepath.Dir(exe), "models", "local", Version)
			if fi, err := os.Stat(dev); err == nil && fi.IsDir() {
				return dev
			}
		}
	}
	return filepath.Join(config.Dir(), "models")
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

// stallTimeout aborts a download when no bytes arrive for this long. A wedged
// connection (lid closed mid-transfer, dropped Wi-Fi) must not block io.Copy
// forever and leave the tray menu stuck at "downloading N%" until restart.
const stallTimeout = 60 * time.Second

// Download fetches a model to Dir() atomically: stream to a temp file, verify
// the sha256, then rename into place. progress (may be nil) is called with the
// fraction downloaded in [0,1]. A no-op if the model is already present. The
// transfer is cancelled if it stalls for stallTimeout.
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Watchdog: every Read resets the timer (via progressReader.onRead); if it
	// fires, no data has arrived for stallTimeout and we cancel the request.
	stall := time.AfterFunc(stallTimeout, cancel)
	defer stall.Stop()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL(), nil)
	if err != nil {
		return fmt.Errorf("download %s: %w", m.Filename, err)
	}
	client := &http.Client{Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}}
	resp, err := client.Do(req)
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
	pr := &progressReader{r: resp.Body, total: m.SizeBytes, cb: progress, onRead: func() { stall.Reset(stallTimeout) }}
	if _, err := io.Copy(io.MultiWriter(tmp, h), pr); err != nil {
		tmp.Close()
		if ctx.Err() != nil {
			return fmt.Errorf("download %s: stalled — no data for %s", m.Filename, stallTimeout)
		}
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

// progressReader reports download progress at most ~10x/sec via cb, and calls
// onRead on every non-empty read so the caller can reset a stall watchdog.
type progressReader struct {
	r      io.Reader
	total  int64
	read   int64
	cb     func(float64)
	onRead func()
	lastAt time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 && p.onRead != nil {
		p.onRead()
	}
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
