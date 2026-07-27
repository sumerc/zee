//go:build darwin

package permissions

/*
#cgo LDFLAGS: -framework AVFoundation -framework ApplicationServices -framework CoreFoundation
int permMicStatus(void);
int permMicRequest(void);
int permAXTrusted(void);
int permAXPrompt(void);
*/
import "C"

import "os/exec"

// OpenAccessibilitySettings opens System Settings at Privacy & Security →
// Accessibility. The AX prompt is one-shot — once dismissed macOS never shows
// it again — so deep-linking the pane is the reliable way to get the toggle in
// front of the user.
func OpenAccessibilitySettings() {
	exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility").Start()
}

// OpenMicrophoneSettings opens System Settings at Privacy & Security →
// Microphone (for the previously-denied case, which macOS won't re-prompt).
func OpenMicrophoneSettings() {
	exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone").Start()
}

// MicrophoneStatus reports the current microphone authorization without prompting.
func MicrophoneStatus() MicStatus { return micStatusFromC(int(C.permMicStatus())) }

// RequestMicrophone triggers the microphone prompt (when not yet determined) and
// blocks until there is a definitive answer. If the user previously denied,
// macOS will not prompt again and this returns MicDenied immediately.
func RequestMicrophone() MicStatus {
	if C.permMicRequest() == 1 {
		return MicGranted
	}
	return MicDenied
}

// HasAccessibility reports whether this process is trusted for the Accessibility
// API (global hotkey monitoring + synthetic paste), without prompting.
func HasAccessibility() bool { return C.permAXTrusted() == 1 }

// RequestAccessibility shows the system prompt directing the user to grant
// Accessibility in System Settings and returns the trust state at call time
// (almost always false the first time — the grant lands asynchronously, so poll
// HasAccessibility afterwards).
func RequestAccessibility() bool { return C.permAXPrompt() == 1 }

func micStatusFromC(s int) MicStatus {
	switch s {
	case 3:
		return MicGranted
	case 2, 1: // denied or restricted — both mean "cannot record, no prompt"
		return MicDenied
	default:
		return MicNotDetermined
	}
}
