//go:build !darwin || !arm64

package s1

import "errors"

// Available reports whether local S1 normalization is compiled in.
func Available() bool { return false }

// Ctx is the no-op stand-in on platforms without the llama.cpp build.
type Ctx struct{}

func New(path string) (*Ctx, error) {
	return nil, errors.New("s1: local normalization not available on this platform")
}

func (c *Ctx) Normalize(text string) (string, error) { return text, nil }
func (c *Ctx) Close()                                {}
