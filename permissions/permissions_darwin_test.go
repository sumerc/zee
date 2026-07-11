//go:build darwin

package permissions

import "testing"

func TestMicStatusFromC(t *testing.T) {
	cases := map[int]MicStatus{
		0: MicNotDetermined, // notDetermined
		1: MicDenied,        // restricted
		2: MicDenied,        // denied
		3: MicGranted,       // authorized
		9: MicNotDetermined, // unknown → treat as not-yet-decided
	}
	for in, want := range cases {
		if got := micStatusFromC(in); got != want {
			t.Errorf("micStatusFromC(%d) = %v, want %v", in, got, want)
		}
	}
}

func TestMicStatusString(t *testing.T) {
	for s, want := range map[MicStatus]string{
		MicGranted:       "granted",
		MicDenied:        "denied",
		MicNotDetermined: "not determined",
	} {
		if got := s.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", s, got, want)
		}
	}
}
