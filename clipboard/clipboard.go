package clipboard

import cb "github.com/atotto/clipboard"

func Copy(text string) error {
	return cb.WriteAll(text)
}
