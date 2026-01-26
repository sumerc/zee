package hotkey

type Hotkey interface {
	Register() error
	Unregister()
	Keydown() <-chan struct{}
	Keyup() <-chan struct{}
}
