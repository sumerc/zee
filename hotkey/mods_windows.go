//go:build windows

package hotkey

import "golang.design/x/hotkey"

// libMods maps our modifier names to Windows modifiers. The names are
// macOS-centric (config is shared): "option" is the Alt key, "cmd" the Win key.
// Keep in sync with validateCombo's accepted set in hotkey_other.go.
var libMods = map[string]hotkey.Modifier{
	"ctrl":   hotkey.ModCtrl,
	"shift":  hotkey.ModShift,
	"option": hotkey.ModAlt,
	"cmd":    hotkey.ModWin,
}
