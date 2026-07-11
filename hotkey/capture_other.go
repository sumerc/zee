//go:build !linux && !darwin

package hotkey

import "errors"

// captureChord is unsupported on platforms without a global key monitor.
func captureChord(cancel <-chan struct{}) (Combo, error) {
	return Combo{}, errors.New("hotkey capture not supported on this platform")
}
