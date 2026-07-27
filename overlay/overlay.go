// Package overlay draws the on-screen recording indicator: a Dynamic
// Island-style panel that hangs off the top of the main display, showing what
// the pipeline is doing and animating the live microphone level.
//
// The panel replaces what the tray icon and the no-speech beep used to say:
// the state is written out, and whether the mic hears you is visible in the
// bars instead of audible in a warning tone.
package overlay

// State is what the overlay tells the user right now.
type State int

const (
	Recording    State = iota // capturing; bars follow the mic
	Silent                    // capturing, but nothing heard for a while
	Transcribing              // audio sent, waiting for text
)

func (s State) label() string {
	switch s {
	case Silent:
		return "Are you talking?"
	case Transcribing:
		return "Transcribing…"
	default:
		return "Recording"
	}
}

// accent is the colour of the status dot and the level bars.
func (s State) accent() (r, g, b float64) {
	switch s {
	case Silent:
		return 1.00, 0.62, 0.04 // amber
	case Transcribing:
		return 0.04, 0.52, 1.00 // blue
	default:
		return 1.00, 0.27, 0.23 // red
	}
}

// Levels is how many samples the scrolling meter keeps on screen.
const Levels = 30

// Show reveals the overlay, expanding it out of the notch with an empty meter
// and back in the Recording state — a session that ended while silent must not
// reopen still saying so. It is idempotent and safe from any goroutine, as is
// every other call in this package. The panel is fixed top-centre on the main
// display and ignores the mouse.
func Show() {
	SetState(Recording)
	show()
}

// Hide collapses and removes the overlay.
func Hide() { hide() }

// showsMeter reports whether the level meter means anything in this state. It
// does not once the mic is closed: there is no more audio to draw, and a row
// of flat bars only says the meter has stopped. The panel drops the row and
// shrinks to fit instead.
func (s State) showsMeter() bool { return s != Transcribing }

// SetState swaps the label and the accent colour, and grows or shrinks the
// panel to match what that state has to show.
func SetState(s State) {
	r, g, b := s.accent()
	setState(s.label(), r, g, b, s.showsMeter())
}

// PushLevel appends one microphone sample (0..1) to the scrolling meter. Call
// it at whatever rate the capture loop produces frames; the overlay keeps the
// last Levels samples and scrolls the rest off the left edge.
func PushLevel(v float32) {
	switch {
	case v < 0:
		v = 0
	case v > 1:
		v = 1
	}
	pushLevel(v)
}

// Run takes over the calling thread with the platform event loop. Only
// standalone tools need it — inside zee the tray already owns the main loop,
// and every call above dispatches onto it.
func Run() { run() }
