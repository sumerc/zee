package clipboard

// Read returns the clipboard's current text, or "" when it holds none.
func Read() (string, error) { return read() }

// Copy replaces the clipboard contents with text.
func Copy(text string) error { return write(text) }
