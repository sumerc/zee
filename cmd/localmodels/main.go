// Command localmodels is the CLI face of the localmodel registry
// (localmodel.go, the single source of truth). Two verbs, both thin projections
// of that registry:
//
//	localmodels download   fetch the prefetch models into the dev folder
//	                       (models/parakeet/<Version>); used by `make build`.
//	localmodels manifest   print the registry as flat text (filename, sha256,
//	                       prefetch) — written to localmodel/manifest.txt by
//	                       `make manifest`, which install.sh reads.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"zee/localmodel"
)

func main() {
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "download":
		download()
	case "manifest":
		fmt.Print(localmodel.Manifest())
	default:
		fmt.Fprintln(os.Stderr, "usage: localmodels <download|manifest>")
		os.Exit(2)
	}
}

// download fetches the mandatory (PreFetch) models into the versioned dev folder.
// Opt-in models are skipped — fetch those from the tray at runtime. Best-effort:
// a failure warns but never fails the build (a dev may copy the ggufs in
// manually, or the release isn't up yet).
func download() {
	dir := filepath.Join("models", "parakeet", localmodel.Version)
	os.Setenv("ZEE_MODELS_DIR", dir) // force the dev folder regardless of cwd state

	for _, m := range localmodel.All() {
		if !m.PreFetch {
			continue
		}
		if localmodel.Present(m) {
			fmt.Printf("✓ %s present\n", m.Filename)
			continue
		}
		fmt.Printf("↓ %s (%s)...\n", m.Filename, m.HumanSize())
		if err := localmodel.Download(m, nil); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: %v\n", err)
			fmt.Fprintf(os.Stderr, "  place %s in %s/ manually, or publish the models-%s release\n",
				m.Filename, dir, localmodel.Version)
		}
	}
}
