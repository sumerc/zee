//go:build windows

package hotkey

import "golang.design/x/hotkey"

// modAlias maps user-facing modifier tokens to Windows modifiers.
var modAlias = map[string]hotkey.Modifier{
	"ctrl":    hotkey.ModCtrl,
	"control": hotkey.ModCtrl,
	"shift":   hotkey.ModShift,
	"alt":     hotkey.ModAlt,
	"option":  hotkey.ModAlt,
	"opt":     hotkey.ModAlt,
	"cmd":     hotkey.ModWin,
	"command": hotkey.ModWin,
	"super":   hotkey.ModWin,
	"meta":    hotkey.ModWin,
	"win":     hotkey.ModWin,
}
