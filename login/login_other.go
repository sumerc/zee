//go:build !darwin

package login

func Enabled() bool    { return false }
func Enable() error    { return nil }
func Disable() error   { return nil }
