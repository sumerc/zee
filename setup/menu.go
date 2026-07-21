package setup

import (
	"fmt"
	"os"
	"strings"
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

// exitInterrupted is the shared Ctrl+C epilogue: huh maps Ctrl+C to abort
// (runPrompt routes it here) and begin's SIGINT handler catches it anywhere
// else. Every step persists as it completes, so nothing is lost — say so and
// leave with the conventional interrupt code.
func exitInterrupted() {
	fmt.Print("\r\n")
	fmt.Println("Interrupted — everything configured so far is saved. Re-run `zee setup` to continue.")
	fmt.Println()
	os.Exit(130)
}
