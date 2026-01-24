//go:build darwin

package clipboard

// Type copies text to the system clipboard and pastes it via Cmd+V.
func Type(text string) error {
	if err := Copy(text); err != nil {
		return err
	}
	return Paste()
}
