//go:build !darwin

package alert

func Error(_ string)            {}
func Warn(_ string)             {}
func Info(_ string)             {}
func Confirm(_, _ string) bool  { return false }
