//go:build darwin

package tray

import _ "embed"

// Idle is a template image (shape only — AppKit tints it to match the bar). The
// state icons are the opposite: a single saturated dot, which reads on a light
// and a dark bar alike, so they ship untinted and unvaried. Regenerate with
// `go run ./packaging/mktrayicons`.
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
