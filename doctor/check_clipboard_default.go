//go:build linux && !nativeclipboard

package doctor

import (
	"fmt"

	"zee/clipboard"
)

func checkClipboardCopy() bool {
	fmt.Println()
	fmt.Println("[5/6] Keystroke output (uinput init)")

	if err := clipboard.Init(); err != nil {
		fmt.Printf("  FAIL: %v\n", err)
		fmt.Println("  Fix with: sudo chmod 660 /dev/uinput && sudo chgrp input /dev/uinput")
		return false
	}

	fmt.Println("  PASS: uinput device initialized")
	return true
}

func checkClipboardPaste() bool {
	fmt.Println()
	fmt.Println("[6/6] Keystroke output (type)")

	msg, err := clipboard.Verify()
	if err != nil {
		fmt.Printf("  FAIL: %v\n", err)
		return false
	}

	fmt.Printf("  PASS: %s\n", msg)
	return true
}
