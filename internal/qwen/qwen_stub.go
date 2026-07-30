//go:build !darwin || !arm64

// Stub for platforms without the qwen-asr static lib (everything except
// darwin/arm64). Available() is false; nothing links any C dependency, so the
// universal-binary release pipeline and Linux/Intel builds are untouched.
package qwen

import "errors"

// Available reports whether local Qwen transcription is compiled in.
func Available() bool { return false }

// Languages is empty when the engine is not compiled in.
func Languages() []string { return nil }

// Ctx is an empty placeholder on unsupported platforms.
type Ctx struct{}

var errUnavailable = errors.New("qwen: local transcription is only available on Apple Silicon")

func New(string) (*Ctx, error) { return nil, errUnavailable }

func (c *Ctx) Transcribe([]float32, string, string) (string, error) { return "", errUnavailable }

func (c *Ctx) Stats() (total, encode, decode float64) { return 0, 0, 0 }

func (c *Ctx) Close() {}
