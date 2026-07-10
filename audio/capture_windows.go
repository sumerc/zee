//go:build windows

package audio

import "errors"

// Windows audio capture is not implemented. The app currently ships for
// darwin/arm64 only (see .goreleaser.yml); these stubs exist solely so the
// package compiles under GOOS=windows. When Windows becomes a real target,
// give it its own backend here — it need not mirror darwin's malgo path.
func NewContext() (Context, error) {
	return nil, errors.New("audio: capture not implemented on windows")
}
