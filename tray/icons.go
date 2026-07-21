//go:build darwin

package tray

import _ "embed"

// Idle is a template image (macOS tints it to match the menu bar). The colored
// state icons can't be templates — template mode discards color — so each ships
// in two variants: Hi (white glyph, for a dark menu bar) and Lo (dark glyph,
// for a light menu bar), picked at set-time by menu bar appearance.
var (
	//go:embed icon_idle.png
	iconIdle []byte
	iconIdleHi = iconIdle

	//go:embed icon_rec.png
	iconRecHi []byte
	//go:embed icon_rec_light.png
	iconRecLo []byte

	//go:embed icon_warn.png
	iconWarnHi []byte
	//go:embed icon_warn_light.png
	iconWarnLo []byte

	//go:embed icon_busy.png
	iconBusyHi []byte
	//go:embed icon_busy_light.png
	iconBusyLo []byte
)
