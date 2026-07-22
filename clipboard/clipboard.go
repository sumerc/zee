package clipboard

import (
	"os"

	cb "github.com/atotto/clipboard"
)

// disabled is a test hook: atotto forks pbcopy/pbpaste per call, which is
// O(RSS) and stalls the run loop (tap-misfire suspect).
var disabled = os.Getenv("ZEE_NO_CLIPBOARD") != ""

func Read() (string, error) {
	if disabled {
		return "", nil
	}
	return cb.ReadAll()
}

func Copy(text string) error {
	if disabled {
		return nil
	}
	return cb.WriteAll(text)
}
