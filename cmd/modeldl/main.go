// Command modeldl downloads the mandatory (PreFetch) local models into the
// versioned dev folder (models/parakeet/<Version>), used by `make
// download-models` and the build step. Opt-in models are skipped — fetch those
// from the tray at runtime. Best-effort: a failure warns but never fails the
// build (a dev may symlink/copy the ggufs instead, or the release isn't up yet).
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"zee/localmodel"
)

func main() {
	dir := filepath.Join("models", "parakeet", localmodel.Version)
	os.Setenv("ZEE_MODELS_DIR", dir) // force the dev folder regardless of cwd state

	for _, m := range localmodel.All() {
		if !m.PreFetch {
			continue // opt-in — not auto-downloaded
		}
		if localmodel.Present(m) {
			fmt.Printf("✓ %s present\n", m.Filename)
			continue
		}
		fmt.Printf("↓ %s (%d MB)...\n", m.Filename, m.SizeBytes>>20)
		if err := localmodel.Download(m, nil); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: %v\n", err)
			fmt.Fprintf(os.Stderr, "  place %s in %s/ manually, or publish the models-%s release\n",
				m.Filename, dir, localmodel.Version)
		}
	}
}
