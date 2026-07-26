//go:build darwin

package hotkey

/*
#cgo darwin LDFLAGS: -framework Cocoa
void startHotkeyCapture(void);
void stopHotkeyCapture(void);
*/
import "C"

import (
	"fmt"
	"time"
)

// macOS device-independent modifier flag bits (NSEventModifierFlag*).
const (
	nsShift   = 1 << 17
	nsControl = 1 << 18
	nsOption  = 1 << 19
	nsCommand = 1 << 20
)

const kVKEscape = 53

type rawChord struct {
	key      int
	mods     uint64
	canceled bool
}

// captureCh delivers chords from the C monitor callback. Only one capture runs
// at a time (the tray serializes Change Hotkey…), so a single channel suffices.
var captureCh = make(chan rawChord, 1)

//export goHotkeyCaptured
func goHotkeyCaptured(keycode C.int, mods C.ulong) {
	m := uint64(mods)
	hasMod := m&(nsControl|nsShift|nsOption|nsCommand) != 0
	// Bare Escape cancels; Escape WITH a modifier is a valid hotkey.
	if int(keycode) == kVKEscape && !hasMod {
		trySend(rawChord{canceled: true})
		return
	}
	if !hasMod {
		return // ignore unmodified keys, keep listening
	}
	trySend(rawChord{key: int(keycode), mods: m})
}

func trySend(c rawChord) {
	select {
	case captureCh <- c:
	default:
	}
}

func captureChord(cancel <-chan struct{}) (Combo, error) {
	select { // drain any stale chord
	case <-captureCh:
	default:
	}
	C.startHotkeyCapture()
	defer C.stopHotkeyCapture()

	select {
	case c := <-captureCh:
		if c.canceled {
			return Combo{}, ErrCaptureCanceled
		}
		return chordToCombo(c), nil
	case <-cancel:
		return Combo{}, ErrCaptureCanceled
	case <-time.After(captureTimeout):
		return Combo{}, ErrCaptureCanceled
	}
}

func chordToCombo(c rawChord) Combo {
	var mods []string
	for _, m := range []struct {
		bit  uint64
		name string
	}{
		{nsControl, "ctrl"}, {nsOption, "option"}, {nsShift, "shift"}, {nsCommand, "cmd"},
	} {
		if c.mods&m.bit != 0 {
			mods = append(mods, m.name)
		}
	}
	return Combo{Mods: mods, Key: c.key, Label: ComboLabel(mods, keyGlyph(c.key))}
}

// keyGlyph maps a macOS virtual keycode to a display string. Unmapped keys fall
// back to a generic label (registration still works — this is display only).
func keyGlyph(code int) string {
	if g, ok := darwinKeyGlyphs[code]; ok {
		return g
	}
	return fmt.Sprintf("Key%d", code)
}

var darwinKeyGlyphs = map[int]string{
	0: "A", 1: "S", 2: "D", 3: "F", 4: "H", 5: "G", 6: "Z", 7: "X", 8: "C", 9: "V",
	11: "B", 12: "Q", 13: "W", 14: "E", 15: "R", 16: "Y", 17: "T",
	18: "1", 19: "2", 20: "3", 21: "4", 22: "6", 23: "5", 25: "9", 26: "7", 28: "8", 29: "0",
	31: "O", 32: "U", 34: "I", 35: "P", 37: "L", 38: "J", 40: "K", 45: "N", 46: "M",
	36: "Return", 48: "Tab", 49: "Space", 51: "Delete", 53: "Esc",
	123: "←", 124: "→", 125: "↓", 126: "↑",
	96: "F5", 97: "F6", 98: "F7", 99: "F3", 100: "F8", 101: "F9",
	103: "F11", 109: "F10", 111: "F12", 118: "F4", 120: "F2", 122: "F1",
}
