//go:build darwin && arm64

package whisper_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// zee builds whisper.cpp from a pinned submodule plus the in-tree patches in
// patches/whisper.cpp, applied by `make whisper-lib`. A `git submodule update`
// resets the checkout and drops them, and nothing else notices: the build still
// succeeds and transcripts are still correct — auto-detect just quietly costs
// twice what it should again.
//
// The check is the same one the Makefile uses: reverse-applying the whole patch
// set must succeed. git apply reads the patch files themselves, so the result
// does not depend on `git diff` output format, user git config, or how shallow
// the submodule clone is (abbreviated hashes vary with object count). Submodule
// bumps are the Makefile's WHISPER_BASE pin's job, not this test's.
func TestWhisperPatchesApplied(t *testing.T) {
	root := filepath.Join("..", "..")
	sub := filepath.Join(root, "third_party", "whisper.cpp")
	if _, err := os.Stat(filepath.Join(sub, ".git")); err != nil {
		t.Skipf("whisper.cpp submodule not checked out: %v", err)
	}

	patches, err := filepath.Glob(filepath.Join(root, "patches", "whisper.cpp", "*.patch"))
	if err != nil || len(patches) == 0 {
		t.Fatalf("no patches found in patches/whisper.cpp (err=%v)", err)
	}

	// Read every file the patches touch. go test caches a PASS keyed on the
	// files a test opens, and a git subprocess opens them behind its back —
	// without this, a reset or hand-edit of the submodule would replay a
	// cached PASS.
	for _, p := range patches {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read patch: %v", err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if f, ok := strings.CutPrefix(line, "+++ b/"); ok {
				if _, err := os.ReadFile(filepath.Join(sub, f)); err != nil {
					t.Fatalf("patched file %s: %v", f, err)
				}
			}
		}
	}

	abs := make([]string, len(patches))
	for i, p := range patches {
		abs[i], _ = filepath.Abs(p)
	}
	slices.Reverse(abs) // undo in reverse order of application
	args := append([]string{"-C", sub, "apply", "--reverse", "--check"}, abs...)
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		t.Fatalf("third_party/whisper.cpp does not carry patches/whisper.cpp/*.patch: %v\n%s\n"+
			"Run `make whisper-lib` to reapply them. If the submodule source was edited\n"+
			"on purpose, regenerate the patch from it:\n"+
			"    git -C third_party/whisper.cpp diff > patches/whisper.cpp/0001-reuse-detect-encoder-output.patch",
			err, out)
	}
}
