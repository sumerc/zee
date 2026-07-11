// Package permissions is the single, platform-neutral surface for the OS
// privacy permissions Zee needs: Microphone (to record) and Accessibility (to
// register a global hotkey and synthesize the paste keystroke). It owns the
// *prompting* flow used by the setup wizard; passive checks elsewhere (e.g.
// clipboard) are independent.
//
// On macOS these are TCC-gated and can only be granted when the app itself makes
// the call, so the wizard runs as the installed bundle. On other platforms the
// functions report "granted"/"trusted" so callers stay platform-neutral.
package permissions

// MicStatus is the microphone authorization state.
type MicStatus int

const (
	MicNotDetermined MicStatus = iota // never asked; a prompt will be shown
	MicDenied                         // user denied; macOS will not prompt again
	MicGranted                        // authorized
)

func (s MicStatus) String() string {
	switch s {
	case MicGranted:
		return "granted"
	case MicDenied:
		return "denied"
	default:
		return "not determined"
	}
}
