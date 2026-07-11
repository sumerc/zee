package hotkey

type FakeHotkey struct {
	keydown chan struct{}
	keyup   chan struct{}
	combo   Combo

	// NextCapture is returned by the next Capture call (tests set this to script
	// a recorded chord). If zero, Capture returns ErrCaptureCanceled.
	NextCapture Combo
}

func NewFake() *FakeHotkey {
	return &FakeHotkey{
		keydown: make(chan struct{}, 1),
		keyup:   make(chan struct{}, 1),
		combo:   Combo{Mods: []string{"ctrl", "shift"}, Key: 49, Label: "⌃⇧Space"},
	}
}

func (f *FakeHotkey) Register() error          { return nil }
func (f *FakeHotkey) Unregister()              {}
func (f *FakeHotkey) Keydown() <-chan struct{} { return f.keydown }
func (f *FakeHotkey) Keyup() <-chan struct{}   { return f.keyup }
func (f *FakeHotkey) Current() Combo           { return f.combo }

func (f *FakeHotkey) Rebind(c Combo) error {
	if !hasModifier(c.Mods) {
		return ErrCaptureCanceled
	}
	f.combo = c
	return nil
}

func (f *FakeHotkey) Capture(cancel <-chan struct{}) (Combo, error) {
	if f.NextCapture.IsZero() {
		return Combo{}, ErrCaptureCanceled
	}
	return f.NextCapture, nil
}

func (f *FakeHotkey) SimKeydown() { f.keydown <- struct{}{} }
func (f *FakeHotkey) SimKeyup()   { f.keyup <- struct{}{} }
