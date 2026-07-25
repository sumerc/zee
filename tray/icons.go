//go:build darwin

package tray

import _ "embed"

// Every menu-bar icon is a macOS *template* image: it carries shape only, and
// AppKit tints it to match the bar it is drawn on. That is the one thing that
// stays correct on Tahoe's glass menu bar, whose appearance is derived from the
// wallpaper behind it rather than from the Light/Dark setting — so state is
// encoded as a badge SHAPE (disc = recording, ring = warning, bar =
// transcribing), never as a colour.
//
// The colored icons this replaced shipped Hi/Lo variants chosen from
// AppleInterfaceStyle, which answers the wrong question on Tahoe and rendered a
// black glyph on a dark bar. Regenerate with `go run ./packaging/mktrayicons`.
var (
	//go:embed icon_idle.png
	iconIdle []byte

	//go:embed icon_rec.png
	iconRec []byte

	//go:embed icon_warn.png
	iconWarn []byte

	//go:embed icon_busy.png
	iconBusy []byte
)
