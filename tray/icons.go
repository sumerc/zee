//go:build darwin

package tray

import _ "embed"

//go:embed icon_idle_22.png
var iconIdle []byte

//go:embed icon_idle_44.png
var iconIdleHi []byte

//go:embed icon_rec_44.png
var iconRecHi []byte

//go:embed icon_warn_44.png
var iconWarnHi []byte
