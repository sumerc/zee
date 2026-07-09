package hotkey

import "strings"

type Hotkey interface {
	Register() error
	Unregister()
	Keydown() <-chan struct{}
	Keyup() <-chan struct{}
}

// DefaultCombo is the built-in hotkey used when none is configured.
const DefaultCombo = "ctrl+shift+space"

// canonical display names for known tokens (modifiers and a few named keys).
var displayNames = map[string]string{
	"ctrl": "Control", "control": "Control",
	"shift": "Shift",
	"alt":   "Option", "option": "Option", "opt": "Option",
	"cmd": "Command", "command": "Command", "super": "Command", "meta": "Command", "win": "Command",
	"space": "Space", "spacebar": "Space",
	"enter": "Enter", "return": "Enter",
	"tab": "Tab", "esc": "Escape", "escape": "Escape",
	"up": "Up", "down": "Down", "left": "Left", "right": "Right",
}

// modOrder gives modifiers a stable display order.
var modOrder = map[string]int{
	"Control": 0, "Option": 1, "Shift": 2, "Command": 3,
}

// FormatCombo turns a raw combo string (e.g. "ctrl+shift+space") into a
// human-friendly, stably-ordered label (e.g. "Control+Shift+Space"). It does no
// validation — unknown tokens are simply title-cased and appended. An empty
// input yields the label for DefaultCombo.
func FormatCombo(combo string) string {
	if strings.TrimSpace(combo) == "" {
		combo = DefaultCombo
	}
	var mods []string
	var keys []string
	for _, tok := range strings.Split(combo, "+") {
		tok = strings.TrimSpace(strings.ToLower(tok))
		if tok == "" {
			continue
		}
		name, known := displayNames[tok]
		if !known {
			name = strings.ToUpper(tok[:1]) + tok[1:]
		}
		if _, isMod := modOrder[name]; isMod {
			mods = append(mods, name)
		} else {
			keys = append(keys, name)
		}
	}
	sortMods(mods)
	return strings.Join(append(mods, keys...), "+")
}

func sortMods(mods []string) {
	// small insertion sort keyed by modOrder — order matters, len is tiny
	for i := 1; i < len(mods); i++ {
		for j := i; j > 0 && modOrder[mods[j-1]] > modOrder[mods[j]]; j-- {
			mods[j-1], mods[j] = mods[j], mods[j-1]
		}
	}
}
