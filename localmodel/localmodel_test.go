package localmodel

import (
	"regexp"
	"strings"
	"testing"
)

// TestSourceURL: a model with a Source downloads from it, pinned to a commit
// so its SHA256 cannot drift; the rest come from the models release.
func TestSourceURL(t *testing.T) {
	for _, m := range All() {
		if m.Source == "" {
			if want := baseURL + m.Filename; m.URL() != want {
				t.Errorf("%s: URL = %q, want %q", m.ID, m.URL(), want)
			}
			continue
		}
		if m.URL() != m.Source || !strings.HasSuffix(m.Source, "/"+m.Filename) {
			t.Errorf("%s: URL = %q, want Source ending in /%s", m.ID, m.URL(), m.Filename)
		}
		if !regexp.MustCompile(`/resolve/[0-9a-f]{40}/`).MatchString(m.Source) {
			t.Errorf("%s: Source %q is not pinned to a commit", m.ID, m.Source)
		}
	}
}
