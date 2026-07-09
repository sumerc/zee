//go:build darwin

package hotkey

import "golang.design/x/hotkey"

// modAlias maps user-facing modifier tokens to macOS modifiers.
var modAlias = map[string]hotkey.Modifier{
	"ctrl":    hotkey.ModCtrl,
	"control": hotkey.ModCtrl,
	"shift":   hotkey.ModShift,
	"alt":     hotkey.ModOption,
	"option":  hotkey.ModOption,
	"opt":     hotkey.ModOption,
	"cmd":     hotkey.ModCmd,
	"command": hotkey.ModCmd,
	"super":   hotkey.ModCmd,
	"meta":    hotkey.ModCmd,
}
