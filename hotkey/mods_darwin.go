//go:build darwin

package hotkey

import "golang.design/x/hotkey"

// libMods maps our modifier names to macOS Carbon modifiers. Keep in sync with
// validateCombo's accepted set in hotkey_other.go.
var libMods = map[string]hotkey.Modifier{
	"ctrl":   hotkey.ModCtrl,
	"shift":  hotkey.ModShift,
	"option": hotkey.ModOption,
	"cmd":    hotkey.ModCmd,
}
