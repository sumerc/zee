//go:build !linux

package hotkey

import "testing"

// TestValidateCombo guards the bare-key hazard: toLib silently drops unknown
// modifier names, so without validation a typo like "alt" would register the
// bare key (Space) system-wide.
func TestValidateCombo(t *testing.T) {
	tests := []struct {
		name string
		c    Combo
		ok   bool
	}{
		{"default", DefaultCombo(), true},
		{"known mods", Combo{Mods: []string{"cmd", "option"}, Key: 49}, true},
		{"no modifier", Combo{Mods: nil, Key: 49}, false},
		{"unknown modifier alt", Combo{Mods: []string{"alt"}, Key: 49}, false},
		{"one known one unknown", Combo{Mods: []string{"ctrl", "meta"}, Key: 49}, false},
	}
	for _, tt := range tests {
		err := validateCombo(tt.c)
		if (err == nil) != tt.ok {
			t.Errorf("%s: validateCombo err=%v, want ok=%v", tt.name, err, tt.ok)
		}
	}
}
