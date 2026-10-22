package overlay

import (
	"slices"
	"testing"
)

func TestParseHex(t *testing.T) {
	if r, g, b, ok := parseHex("#FF8000"); !ok || r != 1 || g != 128.0/255 || b != 0 {
		t.Errorf("parseHex(#FF8000) = %v %v %v %v", r, g, b, ok)
	}
	for _, bad := range []string{"", "#FFF", "#GG0000", "red"} {
		if _, _, _, ok := parseHex(bad); ok {
			t.Errorf("parseHex(%q) accepted", bad)
		}
	}
	for _, c := range TagColors {
		if _, _, _, ok := parseHex(c); !ok {
			t.Errorf("palette entry %q does not parse", c)
		}
	}
}

// TestPickTagColor: a free palette colour wins over a taken one, matched
// case-insensitively; with none free, any palette colour will do.
func TestPickTagColor(t *testing.T) {
	used := []string{}
	for _, c := range TagColors[1:] {
		used = append(used, c)
	}
	used[0] = "#ffd60a" // lower case still counts as taken
	for range 20 {
		if got := PickTagColor(used); got != TagColors[0] {
			t.Fatalf("PickTagColor = %q, want the only free %q", got, TagColors[0])
		}
	}
	if got := PickTagColor(TagColors); !slices.Contains(TagColors, got) {
		t.Errorf("PickTagColor(all used) = %q, not in the palette", got)
	}
}
