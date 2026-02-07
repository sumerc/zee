package clipboard

import cb "github.com/atotto/clipboard"

func Read() (string, error) {
	return cb.ReadAll()
}

func Copy(text string) error {
	return cb.WriteAll(text)
}
