package main

import (
	"fmt"
	"os"

	"zee/audio"

	"golang.org/x/term"
)

func selectDevice(ctx audio.Context) (*audio.DeviceInfo, error) {
	devices, err := ctx.Devices()
	if err != nil {
		return nil, fmt.Errorf("enumerating devices: %w", err)
	}

	if len(devices) == 0 {
		return nil, fmt.Errorf("no capture devices found")
	}

	if len(devices) == 1 {
		fmt.Printf("Using device: %s\n", devices[0].Name)
		return &devices[0], nil
	}

	// Raw mode for arrow key input
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("setting raw mode: %w", err)
	}
	defer term.Restore(fd, oldState)

	cursor := 0
	renderList := func() {
		// Move cursor up to redraw (except first render)
		fmt.Print("\r\x1b[J") // clear from cursor to end
		fmt.Print("Select input device (↑/↓, Enter to confirm):\r\n\r\n")
		for i, d := range devices {
			if i == cursor {
				fmt.Printf("  \x1b[1;36m▶ %s\x1b[0m\r\n", d.Name)
			} else {
				fmt.Printf("    %s\r\n", d.Name)
			}
		}
	}

	// Initial render
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
				fmt.Printf("\r\n")
				term.Restore(fd, oldState)
				return &devices[cursor], nil
			case 3: // Ctrl+C
				fmt.Printf("\r\n")
				term.Restore(fd, oldState)
				os.Exit(0)
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

		// Redraw: move up to overwrite
		lines := len(devices) + 2
		fmt.Printf("\x1b[%dA", lines)
		renderList()
	}
}
