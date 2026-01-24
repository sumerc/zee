//go:build linux && nativeclipboard

package clipboard

// Type copies text to the system clipboard and pastes it via Ctrl+V.
func Type(text string) error {
	if err := Copy(text); err != nil {
		return err
	}
	return Paste()
}
