//go:build darwin && arm64

package s1_test

import (
	"os"
	"path/filepath"
	"testing"

	"zee/internal/s1"
	"zee/localmodel"
)

// Messy dictation in, written text out. Run with the model present:
//
//	ZEE_MODELS_DIR=$PWD/models/local/v3 go test ./internal/s1 -v
func TestNormalize(t *testing.T) {
	path := filepath.Join(localmodel.Dir(), "s1-mini-q4_k_m.gguf")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("s1-mini model not downloaded: %v", err)
	}
	c, err := s1.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	cases := []string{
		"um so basically i think we should uh move the meeting to three thirty pm on friday and um invite like twenty five people",
		"okay so the total comes to one hundred and forty seven dollars and uh no wait one hundred and fifty seven dollars",
		"send it to john dot smith at example dot com by uh next tuesday",
	}
	for _, in := range cases {
		out, err := c.Normalize(in)
		if err != nil {
			t.Errorf("Normalize(%q): %v", in, err)
			continue
		}
		t.Logf("\n  raw:   %s\n  clean: %s", in, out)
		if out == in {
			t.Errorf("output unchanged for %q", in)
		}
	}
}
