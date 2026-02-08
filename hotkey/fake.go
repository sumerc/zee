package hotkey

type FakeHotkey struct {
	keydown chan struct{}
	keyup   chan struct{}
}

func NewFake() *FakeHotkey {
	return &FakeHotkey{
		keydown: make(chan struct{}, 1),
		keyup:   make(chan struct{}, 1),
	}
}

func (f *FakeHotkey) Register() error   { return nil }
func (f *FakeHotkey) Unregister()       {}
func (f *FakeHotkey) Keydown() <-chan struct{} { return f.keydown }
func (f *FakeHotkey) Keyup() <-chan struct{}   { return f.keyup }

func (f *FakeHotkey) SimKeydown() { f.keydown <- struct{}{} }
func (f *FakeHotkey) SimKeyup()   { f.keyup <- struct{}{} }
