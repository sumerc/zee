package shortcut

// Hotkey provides global shortcut registration with press/release events.
type Hotkey interface {
	Register() error
	Unregister()
	Keydown() <-chan struct{}
	Keyup() <-chan struct{}
}
