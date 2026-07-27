package whisper_test

import (
	"os/exec"
	"strings"
	"testing"
)

// expectedGGML is the ggml commit whisper v1.9.1 was validated against (tag
// v0.13.0). zee does not pin ggml directly — it inherits the pin transitively
// from the parakeet.cpp submodule, which owns both the ggml submodule and the
// in-tree patches applied to it.
//
// That makes the coupling invisible: bumping parakeet.cpp can silently drag
// ggml forward, and whisper would then either fail to compile or (worse)
// compile against shifted struct layouts. This test turns that into one
// explicit failure instead of a wall of C++ errors.
const (
	expectedGGML = "e705c5fed490514458bdd2eaddc43bd098fcce9b"
	ggmlPath     = "third_party/parakeet.cpp/third_party/ggml"
)

func TestGGMLPinUnchanged(t *testing.T) {
	// No path filter: a nested submodule path is not a valid pathspec here, so
	// list everything and pick the ggml line out.
	out, err := exec.Command("git", "-C", "../..", "submodule", "status", "--recursive").Output()
	if err != nil {
		t.Skipf("git submodule status unavailable: %v", err)
	}
	var got string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		// " <sha> <path> (<describe>)"; a leading -/+ marks uninitialised/modified.
		if len(fields) >= 2 && fields[1] == ggmlPath {
			got = strings.TrimLeft(fields[0], "-+U")
			break
		}
	}
	if got == "" {
		t.Skip("ggml submodule not initialised")
	}
	if got != expectedGGML {
		t.Fatalf("ggml pin moved: got %s, want %s\n\n"+
			"whisper v1.9.1 was validated against ggml v0.13.0 (%s). A parakeet.cpp bump\n"+
			"most likely moved it. Re-validate that whisper still builds and transcribes\n"+
			"against the new ggml, then update expectedGGML here.",
			got, expectedGGML, expectedGGML)
	}
}
