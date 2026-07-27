//go:build !darwin

package login

func enabled() bool  { return false }
func enable() error  { return nil }
func disable() error { return nil }
