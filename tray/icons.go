//go:build darwin

package tray

import _ "embed"

// The tray shows one glyph and never changes it. What the app is doing is the
// overlay's job now — it says so in words, in front of the user, instead of
// asking them to notice a coloured dot in the menu bar. This is a template
// image, so AppKit tints it to match the bar without being asked.
//
//go:embed icon.png
var icon []byte
