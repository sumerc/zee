package localmodel

import (
	"os"
	"testing"
)

// TestManifestUpToDate fails if localmodel/manifest.txt has drifted from the
// registry. The committed file must be the exact output of Manifest() —
// regenerate it with `make manifest`. This keeps the bash-readable manifest
// install.sh consumes in lockstep with localmodel.go (the source of truth).
func TestManifestUpToDate(t *testing.T) {
	want := Manifest()
	got, err := os.ReadFile("manifest.txt")
	if err != nil {
		t.Fatalf("read manifest.txt: %v (regenerate with `make manifest`)", err)
	}
	if string(got) != want {
		t.Fatal("localmodel/manifest.txt is stale — regenerate with `make manifest`")
	}
}
