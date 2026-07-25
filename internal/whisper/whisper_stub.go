//go:build !darwin || !arm64

// Stub for platforms without the whisper.cpp static libs (everything except
// darwin/arm64). Available() is false; nothing links any C dependency, so the
// universal-binary release pipeline and Linux/Intel builds are untouched.
package whisper

import "errors"

// Available reports whether local Whisper transcription is compiled in.
func Available() bool { return false }

// Ctx is an empty placeholder on unsupported platforms.
type Ctx struct{}

var errUnavailable = errors.New("whisper: local transcription is only available on Apple Silicon")

func New(string) (*Ctx, error) { return nil, errUnavailable }

func (c *Ctx) Transcribe([]float32, string, string) (string, error) { return "", errUnavailable }

func (c *Ctx) Close() {}
