package setup

import (
	"fmt"
	"os"
	"strings"
	"time"

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

// exitInterrupted is the shared Ctrl+C epilogue (raw-mode readers see byte
// 0x03, cooked-mode prompts deliver SIGINT to begin's handler — both land
// here): every step persists as it completes, so nothing is lost. Say so and
// leave with the conventional interrupt code.
func exitInterrupted() {
	fmt.Print("\r\n")
	fmt.Println("Interrupted — everything configured so far is saved. Re-run `zee setup` to continue.")
	os.Exit(130)
}

// menu renders an arrow-key selectable list (↑/↓ or j/k, Enter to confirm) and
// returns the chosen index. start is the initially-highlighted row, so the
// caller makes start the default entry and Enter alone carries the common path.
// An empty option renders as a blank separator row the cursor skips. On a
// terminal that can't enter raw mode (or non-tty stdin) it falls back to a
// numbered line-read prompt, so the wizard still works over any pipe. Ctrl+C
// exits the process (130).
func menu(title string, options []string, start int) int {
	if start < 0 || start >= len(options) || options[start] == "" {
		start = 0
	}

	fmt.Println() // separate the question from preceding output

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return numberedMenu(title, options, start)
	}
	defer term.Restore(fd, oldState)

	cursor := start
	up := func() {
		for i := cursor - 1; i >= 0; i-- {
			if options[i] != "" {
				cursor = i
				return
			}
		}
	}
	down := func() {
		for i := cursor + 1; i < len(options); i++ {
			if options[i] != "" {
				cursor = i
				return
			}
		}
	}
	render := func() {
		fmt.Printf("\r\x1b[J%s  \x1b[2m(↑/↓, Enter)\x1b[0m\r\n\r\n", title)
		for i, o := range options {
			switch {
			case o == "":
				fmt.Print("\r\n")
			case i == cursor:
				fmt.Printf("  \x1b[1;36m❯ %s\x1b[0m\r\n", o)
			default:
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
				term.Restore(fd, oldState)
				exitInterrupted()
			case 'j':
				down()
			case 'k':
				up()
			}
		} else if n == 3 && buf[0] == 0x1b && buf[1] == '[' {
			switch buf[2] {
			case 'A': // up
				up()
			case 'B': // down
				down()
			}
		}
		fmt.Printf("\x1b[%dA", len(options)+2) // move cursor back up to redraw
		render()
	}
}

// numberedMenu is the non-raw fallback: print a numbered list and read a
// choice. Separator entries ("") are skipped; numbers map to real indices.
func numberedMenu(title string, options []string, start int) int {
	fmt.Printf("%s\n", title)
	var sel []int // displayed number - 1 → index into options
	def := 1
	for i, o := range options {
		if o == "" {
			continue
		}
		sel = append(sel, i)
		marker := " "
		if i == start {
			marker = "*"
			def = len(sel)
		}
		fmt.Printf("  %s %d. %s\n", marker, len(sel), o)
	}
	fmt.Printf("Choice [1-%d] (Enter = %d): ", len(sel), def)

	line := strings.TrimSpace(readLine())
	if line == "" {
		return start
	}
	var n int
	if _, err := fmt.Sscanf(line, "%d", &n); err != nil || n < 1 || n > len(sel) {
		fmt.Println("  (invalid choice, keeping current)")
		return start
	}
	return sel[n-1]
}

// askYesNo prompts for a yes/no answer, returning def when the user just hits
// Enter. Uses simple line input (no raw mode) so it composes with password entry.
func askYesNo(prompt string, def bool) bool {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	fmt.Printf("\n%s %s: ", prompt, hint)
	switch strings.ToLower(strings.TrimSpace(readLine())) {
	case "":
		return def
	case "y", "yes":
		return true
	default:
		return false
	}
}

// readLineTimeout reads one line like readLine but gives up after d (via the
// tty read deadline; if the fd doesn't support deadlines it blocks like
// readLine). Returns whatever arrived — a paste with no trailing newline still
// yields its bytes once the deadline fires.
func readLineTimeout(d time.Duration) string {
	if err := os.Stdin.SetReadDeadline(time.Now().Add(d)); err == nil {
		defer os.Stdin.SetReadDeadline(time.Time{})
	}
	return readLine()
}
