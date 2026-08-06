//go:build darwin && arm64

package whisper_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// zee builds whisper.cpp from a pinned submodule plus the in-tree patches in
// patches/whisper.cpp, applied by `make whisper-lib`. Two ways that goes wrong
// silently, neither of which any other test can see:
//
//   - a `git submodule update` resets the checkout and drops the patches. The
//     build still succeeds and transcripts are still correct — auto-detect just
//     quietly costs twice what it should again.
//   - someone hand-edits the submodule source. `ignore = dirty` in .gitmodules
//     (needed because the patches make the checkout permanently dirty) means
//     `git status` will not show it.
//
// Comparing the submodule's diff against the patch files byte-for-byte catches
// both, and also fires when the submodule is bumped: hunk offsets move, so the
// diff stops matching and a human has to re-validate rather than trust a clean
// `git apply`. The Makefile's WHISPER_BASE pin is the build-time half of this.
func TestWhisperPatchesApplied(t *testing.T) {
	root := filepath.Join("..", "..")
	patchDir := filepath.Join(root, "patches", "whisper.cpp")

	entries, err := os.ReadDir(patchDir)
	if err != nil {
		t.Fatalf("read %s: %v", patchDir, err)
	}
	var want strings.Builder
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".patch" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(patchDir, e.Name()))
		if err != nil {
			t.Fatalf("read patch: %v", err)
		}
		want.Write(b)
	}
	if want.Len() == 0 {
		t.Fatalf("no patches found in %s", patchDir)
	}

	// Same flags the patches are generated with, so the comparison is not at the
	// mercy of the developer's diff config.
	cmd := exec.Command("git", "-c", "core.pager=cat", "-C",
		filepath.Join(root, "third_party", "whisper.cpp"),
		"diff", "--no-color", "--no-ext-diff")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("whisper.cpp submodule not checked out: %v", err)
	}

	if string(out) != want.String() {
		t.Fatalf("third_party/whisper.cpp does not match patches/whisper.cpp/*.patch\n\n"+
			"got %d bytes of diff, want %d.\n\n"+
			"Either the patches were dropped (run `make whisper-lib` to reapply), the\n"+
			"submodule source was hand-edited, or the submodule was bumped and the\n"+
			"patches now apply at different offsets. In the last case re-validate the\n"+
			"optimisation before regenerating — a clean `git apply` does not prove the\n"+
			"patch still does anything:\n"+
			"    ZEE_AC_DEBUG=1 go test ./internal/whisper -run FaultMatrix -v\n"+
			"    make bench-local     # auto must still cost ~the same as forced\n"+
			"    git -C third_party/whisper.cpp -c core.pager=cat diff --no-color \\\n"+
			"        --no-ext-diff > patches/whisper.cpp/0001-reuse-detect-encoder-output.patch",
			len(out), want.Len())
	}
}
