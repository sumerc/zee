package setup

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// readLine reads one line from stdin without any buffering, so it never swallows
// bytes intended for a later prompt (a shared bufio.Reader would) and composes
// with the raw-mode arrow menu, which reads os.Stdin directly. Returns the line
// without its trailing newline; "" on EOF.
func readLine() string {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			if buf[0] != '\r' {
				b.WriteByte(buf[0])
			}
		}
		if err != nil {
			break
		}
	}
	return b.String()
}

// menu renders an arrow-key selectable list (↑/↓ or j/k, Enter to confirm) and
// returns the chosen index. start is the initially-highlighted row. On a
// terminal that can't enter raw mode (or non-tty stdin) it falls back to a
// numbered line-read prompt, so the wizard still works over any pipe. Ctrl+C
// exits the process (130), matching the device picker.
func menu(title string, options []string, start int) int {
	if start < 0 || start >= len(options) {
		start = 0
	}

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return numberedMenu(title, options, start)
	}
	defer term.Restore(fd, oldState)

	cursor := start
	render := func() {
		fmt.Printf("\r\x1b[J%s  \x1b[2m(↑/↓, Enter)\x1b[0m\r\n\r\n", title)
		for i, o := range options {
			if i == cursor {
				fmt.Printf("  \x1b[1;36m❯ %s\x1b[0m\r\n", o)
			} else {
				fmt.Printf("    %s\r\n", o)
			}
		}
	}
	render()

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			term.Restore(fd, oldState)
			return numberedMenu(title, options, cursor)
		}
		if n == 1 {
			switch buf[0] {
			case 13: // Enter
				fmt.Print("\r\n")
				return cursor
			case 3: // Ctrl+C
				fmt.Print("\r\n")
				term.Restore(fd, oldState)
				os.Exit(130)
			case 'j':
				if cursor < len(options)-1 {
					cursor++
				}
			case 'k':
				if cursor > 0 {
					cursor--
				}
			}
		} else if n == 3 && buf[0] == 0x1b && buf[1] == '[' {
			switch buf[2] {
			case 'A': // up
				if cursor > 0 {
					cursor--
				}
			case 'B': // down
				if cursor < len(options)-1 {
					cursor++
				}
			}
		}
		fmt.Printf("\x1b[%dA", len(options)+2) // move cursor back up to redraw
		render()
	}
}

// numberedMenu is the non-raw fallback: print a numbered list and read a choice.
func numberedMenu(title string, options []string, start int) int {
	fmt.Printf("%s\n", title)
	for i, o := range options {
		marker := " "
		if i == start {
			marker = "*"
		}
		fmt.Printf("  %s %d. %s\n", marker, i+1, o)
	}
	fmt.Printf("Choice [1-%d] (Enter = %d): ", len(options), start+1)

	line := strings.TrimSpace(readLine())
	if line == "" {
		return start
	}
	var idx int
	if _, err := fmt.Sscanf(line, "%d", &idx); err != nil || idx < 1 || idx > len(options) {
		fmt.Println("  (invalid choice, keeping current)")
		return start
	}
	return idx - 1
}

// askYesNo prompts for a yes/no answer, returning def when the user just hits
// Enter. Uses simple line input (no raw mode) so it composes with password entry.
func askYesNo(prompt string, def bool) bool {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	fmt.Printf("%s %s: ", prompt, hint)
	switch strings.ToLower(strings.TrimSpace(readLine())) {
	case "":
		return def
	case "y", "yes":
		return true
	default:
		return false
	}
}

// readSecret reads a line without echoing it (for API keys). Falls back to a
// visible read if the terminal can't disable echo. A blank line returns "".
func readSecret(prompt string) string {
	fmt.Print(prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Println()
		if err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return strings.TrimSpace(readLine())
}
