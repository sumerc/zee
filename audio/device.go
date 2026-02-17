package audio

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// SelectDevice presents an interactive device picker and returns the selected device.
// If only one device is available, it returns that device without prompting.
func SelectDevice(ctx Context) (*DeviceInfo, error) {
	devices, err := ctx.Devices()
	if err != nil {
		return nil, fmt.Errorf("enumerating devices: %w", err)
	}

	if len(devices) == 0 {
		return nil, fmt.Errorf("no capture devices found")
	}

	if len(devices) == 1 {
		return &devices[0], nil
	}

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("setting raw mode: %w", err)
	}

	defer term.Restore(fd, oldState)

	cursor := 0
	renderList := func() {
		fmt.Print("\r\x1b[J")
		fmt.Print("Select input device (↑/↓, Enter to confirm):\r\n\r\n")
		for i, d := range devices {
			btTag := ""
			if IsBluetooth(d.Name) {
				btTag = " \x1b[33m[⚠ Lower audio quality]\x1b[0m"
			}
			if i == cursor {
				fmt.Printf("  \x1b[1;36m▶ %s%s\x1b[0m\r\n", d.Name, btTag)
			} else {
				fmt.Printf("    %s%s\r\n", d.Name, btTag)
			}
		}
	}

	renderList()

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return nil, fmt.Errorf("reading input: %w", err)
		}

		if n == 1 {
			switch buf[0] {
			case 13: // Enter
				fmt.Print("\r\n")
				term.Restore(fd, oldState)
				return &devices[cursor], nil
			case 3: // Ctrl+C
				fmt.Print("\r\n")
				term.Restore(fd, oldState)
				os.Exit(130)
			case 'j': // vim down
				if cursor < len(devices)-1 {
					cursor++
				}
			case 'k': // vim up
				if cursor > 0 {
					cursor--
				}
			}
		} else if n == 3 && buf[0] == 0x1b && buf[1] == '[' {
			switch buf[2] {
			case 'A': // Up arrow
				if cursor > 0 {
					cursor--
				}
			case 'B': // Down arrow
				if cursor < len(devices)-1 {
					cursor++
				}
			}
		}

		lines := len(devices) + 2
		fmt.Printf("\x1b[%dA", lines)
		renderList()
	}
}
