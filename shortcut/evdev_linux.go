//go:build linux

package shortcut

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	evKey      = 1
	keyPress   = 1
	keyRelease = 0
	keyLCtrl   = 29
	keyRCtrl   = 97
	keyLShift  = 42
	keyRShift  = 54
	keySpace   = 57
)

// input_event is 24 bytes on 64-bit Linux:
// timeval (16 bytes) + type (2) + code (2) + value (4)
const inputEventSize = 24

type evdevHotkey struct {
	keydown chan struct{}
	keyup   chan struct{}
	files   []*os.File
	stop    chan struct{}
	once    sync.Once
}

// New creates a hotkey using evdev (reads /dev/input directly).
// Requires user to be in the 'input' group.
// Supports hold-to-record: Ctrl+Shift+Space press/release.
func New() Hotkey {
	return &evdevHotkey{
		keydown: make(chan struct{}, 1),
		keyup:   make(chan struct{}, 1),
	}
}

func (h *evdevHotkey) Register() error {
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
		go h.readEvents(f)
	}

	if len(h.files) == 0 {
		return fmt.Errorf("could not open any keyboard device (run: sudo usermod -aG input $USER, then re-login)")
	}

	return nil
}

func (h *evdevHotkey) readEvents(f *os.File) {
	buf := make([]byte, inputEventSize*16)
	var ctrlHeld, shiftHeld, spaceHeld bool

	for {
		select {
		case <-h.stop:
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

			pressed := evValue == keyPress
			released := evValue == keyRelease

			switch evCode {
			case keyLCtrl, keyRCtrl:
				ctrlHeld = pressed || (!released && ctrlHeld)
			case keyLShift, keyRShift:
				shiftHeld = pressed || (!released && shiftHeld)
			case keySpace:
				if pressed && !spaceHeld && ctrlHeld && shiftHeld {
					spaceHeld = true
					select {
					case h.keydown <- struct{}{}:
					default:
					}
				} else if released && spaceHeld {
					spaceHeld = false
					select {
					case h.keyup <- struct{}{}:
					default:
					}
				}
			}
		}
	}
}

func (h *evdevHotkey) Unregister() {
	h.once.Do(func() {
		if h.stop != nil {
			close(h.stop)
		}
		for _, f := range h.files {
			f.Close()
		}
	})
}

func (h *evdevHotkey) Keydown() <-chan struct{} {
	return h.keydown
}

func (h *evdevHotkey) Keyup() <-chan struct{} {
	return h.keyup
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
	// Real keyboards have long key capability bitmaps
	caps := strings.TrimSpace(string(data))
	return len(caps) > 10
}

// Diagnose checks hotkey/evdev access and returns a status message.
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
