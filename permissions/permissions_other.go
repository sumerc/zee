//go:build !darwin

package permissions

// Non-macOS platforms have no TCC gate for these; report everything available so
// callers stay platform-neutral.

func MicrophoneStatus() MicStatus  { return MicGranted }
func RequestMicrophone() MicStatus { return MicGranted }
func HasAccessibility() bool       { return true }
func RequestAccessibility() bool   { return true }
