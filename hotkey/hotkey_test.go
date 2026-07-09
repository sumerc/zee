//go:build !linux

package hotkey

import "testing"

func TestParseComboValid(t *testing.T) {
	cases := []struct {
		in       string
		wantMods int
	}{
		{"ctrl+shift+space", 2},
		{"CTRL+SHIFT+SPACE", 2},
		{" ctrl + shift + space ", 2},
		{"alt+space", 1},
		{"cmd+shift+d", 2},
		{"f8", 0},
		{"ctrl+alt+shift+cmd+k", 4},
		{"", 2}, // empty falls back to DefaultCombo (ctrl+shift+space)
		{"ctrl+ctrl+space", 1}, // duplicate modifier collapses
	}
	for _, c := range cases {
		mods, _, err := parseCombo(c.in)
		if err != nil {
			t.Errorf("parseCombo(%q) unexpected error: %v", c.in, err)
			continue
		}
		if len(mods) != c.wantMods {
			t.Errorf("parseCombo(%q) got %d mods, want %d", c.in, len(mods), c.wantMods)
		}
	}
}

func TestParseComboInvalid(t *testing.T) {
	cases := []string{
		"ctrl+shift",       // no key
		"ctrl",             // no key
		"ctrl+space+enter", // two keys
		"ctrl+shift+nope",  // unknown token
	}
	for _, in := range cases {
		if _, _, err := parseCombo(in); err == nil {
			t.Errorf("parseCombo(%q) expected error, got nil", in)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := Validate("alt+f8"); err != nil {
		t.Errorf("Validate(alt+f8) = %v, want nil", err)
	}
	if err := Validate("garbage"); err == nil {
		t.Errorf("Validate(garbage) = nil, want error")
	}
}

func TestFormatCombo(t *testing.T) {
	cases := map[string]string{
		"ctrl+shift+space": "Control+Shift+Space",
		"shift+ctrl+space": "Control+Shift+Space", // reordered to stable order
		"cmd+alt+k":        "Option+Command+K",
		"alt+space":        "Option+Space",
		"":                 "Control+Shift+Space", // default
		"f8":               "F8",
	}
	for in, want := range cases {
		if got := FormatCombo(in); got != want {
			t.Errorf("FormatCombo(%q) = %q, want %q", in, got, want)
		}
	}
}
