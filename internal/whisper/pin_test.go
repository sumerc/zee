package whisper_test

import (
	"os"
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
const ggmlPath = "third_party/parakeet.cpp/third_party/ggml"

// ggmlBase reads GGML_BASE from the Makefile, the single source of truth for
// which ggml both engines build against.
func ggmlBase(t *testing.T) string {
	b, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "GGML_BASE"); ok {
			return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(v), ":="))
		}
	}
	t.Fatal("GGML_BASE not found in Makefile")
	return ""
}

// TestGGMLPinUnchanged checks the nested ggml checkout is at zee's pin.
// `make` moves it there; a `git submodule update` resets it to parakeet.cpp's
// own (older) pin, and whisper.cpp would then build against a ggml it was not
// validated with.
func TestGGMLPinUnchanged(t *testing.T) {
	want := ggmlBase(t)
	out, err := exec.Command("git", "-C", "../..", "submodule", "status", "--recursive").Output()
	if err != nil {
		t.Skipf("git submodule status unavailable: %v", err)
	}
	var got string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == ggmlPath {
			got = strings.TrimLeft(fields[0], "-+U")
			break
		}
	}
	if got == "" {
		t.Skip("ggml submodule not initialised")
	}
	if got != want {
		t.Fatalf("ggml checkout is at %s, but the Makefile pins GGML_BASE=%s.\n\n"+
			"Run `make whisper-lib` to move it back (a `git submodule update` resets it\n"+
			"to parakeet.cpp's own pin). To bump ggml, change GGML_BASE and re-validate\n"+
			"both engines: transcripts and `make bench-local`.", got, want)
	}
}
