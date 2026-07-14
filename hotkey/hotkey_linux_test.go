//go:build linux

package hotkey

import "testing"

// TestValidateCombo guards the bare-key hazard on the Register path too: a
// hand-edited config with no modifiers (or an unknown name) must not bind a
// bare key like Space system-wide.
func TestValidateCombo(t *testing.T) {
	tests := []struct {
		name string
		c    Combo
		ok   bool
	}{
		{"default", DefaultCombo(), true},
		{"known mods", Combo{Mods: []string{"ctrl", "shift"}, Key: 57}, true},
		{"no modifier", Combo{Mods: nil, Key: 57}, false},
		{"unknown modifier alt", Combo{Mods: []string{"alt"}, Key: 57}, false},
		{"key above uint16 would truncate", Combo{Mods: []string{"ctrl"}, Key: 0x10000}, false},
		{"negative key", Combo{Mods: []string{"ctrl"}, Key: -1}, false},
	}
	for _, tt := range tests {
		err := validateCombo(tt.c)
		if (err == nil) != tt.ok {
			t.Errorf("%s: validateCombo err=%v, want ok=%v", tt.name, err, tt.ok)
		}
	}
}
