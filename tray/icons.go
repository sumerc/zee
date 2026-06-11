//go:build darwin

package tray

import _ "embed"

var (
	//go:embed icon_idle.png
	iconIdle []byte
	iconIdleHi = iconIdle

	//go:embed icon_rec.png
	iconRecHi []byte

	//go:embed icon_warn.png
	iconWarnHi []byte
)
