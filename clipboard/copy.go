//go:build nativeclipboard || darwin

package clipboard

import cb "github.com/atotto/clipboard"

func Copy(text string) error {
	return cb.WriteAll(text)
}

func Read() (string, error) {
	return cb.ReadAll()
}
