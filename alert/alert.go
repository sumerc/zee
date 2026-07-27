// Package alert shows the few blocking dialogs zee needs (a failed
// transcription, a denied action, a missing permission). Platform files
// implement the backend; this file owns the one rule that applies everywhere.
package alert

import (
	"os"
	"strings"
)

// underTest reports whether this binary is a `go test` binary. Dialogs are
// suppressed there: a unit test that exercises a guard (main_test.go drives
// tryStartSession with isRecording set, on purpose) must not blast a real modal
// onto the developer's screen — it looks like the app misfiring, and in CI it
// would hang on a dialog nobody can answer. Keyed off the binary name because
// importing `testing` into production code registers its flags.
var underTest = strings.HasSuffix(os.Args[0], ".test") ||
	strings.Contains(os.Args[0], "/_test/") ||
	os.Getenv("ZEE_NO_DIALOGS") != ""
