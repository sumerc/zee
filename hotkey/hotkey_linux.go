//go:build linux

package hotkey

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	evKey    = 1
	keyPress = 1
	keyRel   = 0
)

const inputEventSize = 24

// Linux evdev scancodes for the modifier groups, by canonical name.
var linuxMods = map[string][]uint16{
	"ctrl":   {29, 97},   // L/R Ctrl
	"shift":  {42, 54},   // L/R Shift
	"option": {56, 100},  // L/R Alt
	"cmd":    {125, 126}, // L/R Meta/Super
}

const keyEsc = 1

// DefaultCombo is the built-in hotkey (Option/Alt+Space); 57 is KEY_SPACE.
func DefaultCombo() Combo {
	return Combo{Mods: []string{"option"}, Key: 57, Label: "⌥Space"}
}

type linuxHotkey struct {
	keydown chan struct{}
	keyup   chan struct{}

	mu    sync.Mutex
	files []*os.File
	stop  chan struct{}
	wg    sync.WaitGroup
	combo Combo
}

func New(c Combo) Hotkey {
	if c.IsZero() {
		c = DefaultCombo()
	}
	return &linuxHotkey{
		keydown: make(chan struct{}, 1),
		keyup:   make(chan struct{}, 1),
		combo:   c,
	}
}

func (h *linuxHotkey) Register() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.startLocked()
}

// validateCombo rejects a combo that can't bind safely: no modifier (would
// grab a bare key system-wide), an unrecognized modifier name, or a key code
// the uint16 evdev cast would silently truncate. Mirrors the darwin/windows
// backend, which validates on both Register and Rebind.
func validateCombo(c Combo) error {
	if !hasModifier(c.Mods) {
		return fmt.Errorf("hotkey needs at least one modifier")
	}
	for _, m := range c.Mods {
		if _, ok := linuxMods[m]; !ok {
			return fmt.Errorf("unsupported hotkey modifier %q on linux", m)
		}
	}
	if c.Key < 0 || c.Key > 0xffff {
		return fmt.Errorf("hotkey key code %d out of range", c.Key)
	}
	return nil
}

func (h *linuxHotkey) startLocked() error {
	if err := validateCombo(h.combo); err != nil {
		return err
	}
	keyCode := uint16(h.combo.Key)
	var modGroups [][]uint16
	for _, m := range h.combo.Mods {
		modGroups = append(modGroups, linuxMods[m])
	}

	keyboards, err := findKeyboards()
	if err != nil {
		return fmt.Errorf("finding keyboards: %w", err)
	}
	if len(keyboards) == 0 {
		return fmt.Errorf("no keyboard devices found (is user in 'input' group?)")
	}

	h.stop = make(chan struct{})
	for _, path := range keyboards {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		h.files = append(h.files, f)
		h.wg.Add(1)
		go h.readEvents(f, modGroups, keyCode, h.stop)
	}
	if len(h.files) == 0 {
		h.stop = nil
		return fmt.Errorf("could not open any keyboard device (run: sudo usermod -aG input $USER, then re-login)")
	}
	return nil
}

func (h *linuxHotkey) stopLocked() {
	if h.stop != nil {
		close(h.stop)
		h.stop = nil
	}
	for _, f := range h.files {
		f.Close()
	}
	h.files = nil
	h.wg.Wait()
}

// readKeyEvents pumps EV_KEY events off an evdev node until stop is closed, the
// read fails, or fn returns false. Both consumers — the live hotkey listener
// and the capture reader — differ only in the state machine they run on top, so
// the record parsing (24-byte stride, type/code/value at offsets 16/18/20)
// lives here once.
func readKeyEvents(f *os.File, stop <-chan struct{}, fn func(code uint16, pressed, released bool) bool) {
	buf := make([]byte, inputEventSize*16)
	for {
		select {
		case <-stop:
			return
		default:
		}
		n, err := f.Read(buf)
		if err != nil {
			return
		}
		for i := 0; i+inputEventSize <= n; i += inputEventSize {
			evType := binary.LittleEndian.Uint16(buf[i+16:])
			evCode := binary.LittleEndian.Uint16(buf[i+18:])
			evValue := int32(binary.LittleEndian.Uint32(buf[i+20:]))
			if evType != evKey {
				continue
			}
			if !fn(evCode, evValue == keyPress, evValue == keyRel) {
				return
			}
		}
	}
}

func (h *linuxHotkey) readEvents(f *os.File, modGroups [][]uint16, keyCode uint16, stop chan struct{}) {
	defer h.wg.Done()
	held := make([]bool, len(modGroups))
	keyHeld := false

	readKeyEvents(f, stop, func(evCode uint16, pressed, released bool) bool {
		for gi, codes := range modGroups {
			for _, c := range codes {
				if evCode == c {
					if pressed {
						held[gi] = true
					} else if released {
						held[gi] = false
					}
				}
			}
		}

		if evCode != keyCode {
			return true
		}
		allMods := true
		for _, hh := range held {
			if !hh {
				allMods = false
				break
			}
		}
		if pressed && !keyHeld && allMods {
			keyHeld = true
			select {
			case h.keydown <- struct{}{}:
			default:
			}
		} else if released && keyHeld {
			keyHeld = false
			select {
			case h.keyup <- struct{}{}:
			default:
			}
		}
		return true
	})
}

func (h *linuxHotkey) Rebind(c Combo) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := validateCombo(c); err != nil {
		return err
	}
	prev := h.combo
	h.stopLocked()
	h.combo = c
	if err := h.startLocked(); err != nil {
		h.combo = prev
		_ = h.startLocked() // restore the previous binding
		return err
	}
	return nil
}

func (h *linuxHotkey) Current() Combo {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.combo
}

func (h *linuxHotkey) Unregister() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopLocked()
}

func (h *linuxHotkey) Keydown() <-chan struct{} { return h.keydown }
func (h *linuxHotkey) Keyup() <-chan struct{}   { return h.keyup }

// Capture records the next modifier+key chord pressed on any keyboard.
func (h *linuxHotkey) Capture(cancel <-chan struct{}) (Combo, error) {
	keyboards, err := findKeyboards()
	if err != nil || len(keyboards) == 0 {
		return Combo{}, ErrCaptureCanceled
	}
	result := make(chan Combo, 1)
	canceled := make(chan struct{}, 1)
	stop := make(chan struct{})
	var files []*os.File
	for _, path := range keyboards {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		files = append(files, f)
		go captureReader(f, result, canceled, stop)
	}
	defer func() {
		close(stop)
		for _, f := range files {
			f.Close()
		}
	}()
	if len(files) == 0 {
		return Combo{}, ErrCaptureCanceled
	}

	select {
	case c := <-result:
		return c, nil
	case <-canceled:
		return Combo{}, ErrCaptureCanceled
	case <-cancel:
		return Combo{}, ErrCaptureCanceled
	case <-time.After(captureTimeout):
		return Combo{}, ErrCaptureCanceled
	}
}

func captureReader(f *os.File, result chan<- Combo, canceled chan<- struct{}, stop chan struct{}) {
	held := map[string]bool{}

	readKeyEvents(f, stop, func(evCode uint16, pressed, released bool) bool {
		if name, ok := modName(evCode); ok {
			if pressed {
				held[name] = true
			} else if released {
				held[name] = false
			}
			return true
		}
		if !pressed {
			return true
		}
		// A non-modifier key was pressed.
		var mods []string
		for _, name := range modOrder {
			if held[name] {
				mods = append(mods, name)
			}
		}
		if len(mods) == 0 {
			if evCode == keyEsc {
				select {
				case canceled <- struct{}{}:
				default:
				}
				return false
			}
			return true // ignore unmodified keys
		}
		select {
		case result <- Combo{Mods: mods, Key: int(evCode), Label: comboLabel(mods, evCode)}:
		default:
		}
		return false
	})
}

func modName(code uint16) (string, bool) {
	for name, codes := range linuxMods {
		for _, c := range codes {
			if c == code {
				return name, true
			}
		}
	}
	return "", false
}

func comboLabel(mods []string, key uint16) string {
	return ComboLabel(mods, keyGlyph(key))
}

func keyGlyph(code uint16) string {
	if g, ok := linuxKeyGlyphs[code]; ok {
		return g
	}
	return fmt.Sprintf("Key%d", code)
}

var linuxKeyGlyphs = map[uint16]string{
	16: "Q", 17: "W", 18: "E", 19: "R", 20: "T", 21: "Y", 22: "U", 23: "I", 24: "O", 25: "P",
	30: "A", 31: "S", 32: "D", 33: "F", 34: "G", 35: "H", 36: "J", 37: "K", 38: "L",
	44: "Z", 45: "X", 46: "C", 47: "V", 48: "B", 49: "N", 50: "M",
	2: "1", 3: "2", 4: "3", 5: "4", 6: "5", 7: "6", 8: "7", 9: "8", 10: "9", 11: "0",
	57: "Space", 15: "Tab", 28: "Enter", 1: "Esc",
	103: "↑", 105: "←", 106: "→", 108: "↓",
	59: "F1", 60: "F2", 61: "F3", 62: "F4", 63: "F5", 64: "F6",
	65: "F7", 66: "F8", 67: "F9", 68: "F10", 87: "F11", 88: "F12",
}

func findKeyboards() ([]string, error) {
	entries, err := os.ReadDir("/dev/input")
	if err != nil {
		return nil, err
	}
	var keyboards []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "event") {
			continue
		}
		path := filepath.Join("/dev/input", e.Name())
		if isKeyboard(e.Name()) {
			keyboards = append(keyboards, path)
		}
	}
	return keyboards, nil
}

func isKeyboard(eventName string) bool {
	capsPath := filepath.Join("/sys/class/input", eventName, "device", "capabilities", "key")
	data, err := os.ReadFile(capsPath)
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(data))) > 10
}

func Diagnose() (string, error) {
	keyboards, err := findKeyboards()
	if err != nil {
		return "", fmt.Errorf("cannot scan input devices: %w", err)
	}
	if len(keyboards) == 0 {
		return "", fmt.Errorf("no keyboard devices found (is user in 'input' group?)")
	}
	var opened string
	for _, path := range keyboards {
		f, err := os.Open(path)
		if err == nil {
			f.Close()
			opened = path
			break
		}
	}
	if opened == "" {
		return "", fmt.Errorf("found %d keyboard(s) but cannot open any (run: sudo usermod -aG input $USER)", len(keyboards))
	}
	return fmt.Sprintf("%d keyboard(s) found, opened %s", len(keyboards), opened), nil
}
