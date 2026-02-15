//go:build !darwin

package tray

func Init() <-chan struct{}                                  { return make(chan struct{}) }
func SetRecording(bool)                                      {}
func OnCopyLast(func())                                      {}
func SetDevices(names []string, selected string, fn func(string)) {}
func Quit()                                                  {}
