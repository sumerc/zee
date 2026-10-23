//go:build !darwin || !arm64

// Stub for platforms without the llama.cpp static lib (everything except
// darwin/arm64). Available() is false and nothing links any C dependency.
package llama

import "errors"

// Available reports whether local LLM inference is compiled in.
func Available() bool { return false }

// Model is never loaded on this platform.
type Model struct{}

var errUnavailable = errors.New("llama: local LLM inference is not compiled in on this platform")

// Load always fails on this platform.
func Load(string) (*Model, error) { return nil, errUnavailable }

// Generate always fails on this platform.
func (*Model) Generate(string, int) (string, error) { return "", errUnavailable }

// Close does nothing on this platform.
func (*Model) Close() {}
