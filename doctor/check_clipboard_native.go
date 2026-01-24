//go:build nativeclipboard || darwin

package doctor

import (
	"fmt"
	"time"

	"ses9000/clipboard"
)

func checkClipboardCopy() bool {
	fmt.Println()
	fmt.Println("[5/6] Clipboard copy")

	testStr := fmt.Sprintf("ses9000-doctor-%d", time.Now().UnixNano())

	type cbResult struct {
		readback string
		err      error
		phase    string
	}
	ch := make(chan cbResult, 1)
	go func() {
		if err := clipboard.Copy(testStr); err != nil {
			ch <- cbResult{err: err, phase: "write"}
			return
		}
		got, err := clipboard.Read()
		if err != nil {
			ch <- cbResult{err: err, phase: "read"}
			return
		}
		ch <- cbResult{readback: got}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			fmt.Printf("  FAIL: clipboard %s failed: %v\n", res.phase, res.err)
			return false
		}
		if res.readback != testStr {
			fmt.Printf("  FAIL: clipboard mismatch: wrote %q, got %q\n", testStr, res.readback)
			return false
		}
		fmt.Println("  PASS: clipboard write/read verified")
		return true
	case <-time.After(3 * time.Second):
		fmt.Println("  FAIL: clipboard timed out (clipboard tool hung - compositor not accessible?)")
		return false
	}
}

func checkClipboardPaste() bool {
	fmt.Println()
	fmt.Println("[6/6] Clipboard paste")

	msg, err := clipboard.Verify()
	if err != nil {
		fmt.Printf("  FAIL: %v\n", err)
		return false
	}

	fmt.Printf("  PASS: %s\n", msg)
	return true
}
