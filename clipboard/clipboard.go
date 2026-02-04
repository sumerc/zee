package clipboard

import (
	"fmt"
	"time"

	cb "github.com/atotto/clipboard"
)

func Read() (string, error) {
	return cb.ReadAll()
}

func Copy(text string) error {
	return cb.WriteAll(text)
}

func CopyAndPasteWithPreserve(text string) error {
	previous, _ := Read()
	if err := Copy(text); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if err := Paste(); err != nil {
		return fmt.Errorf("paste: %w", err)
	}
	if previous != "" {
		go func() {
			time.Sleep(800 * time.Millisecond) // allow paste to reach target app before restoring
			Copy(previous)
		}()
	}
	return nil
}
