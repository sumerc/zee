//go:build !darwin

package tray

func Init() <-chan struct{}                                        { return make(chan struct{}) }
func SetRecording(bool)                                            {}
func OnCopyLast(func())                                            {}
func OnRecord(start, stop func())                                  {}
func SetDevices(names []string, selected string, fn func(string))  {}
func RefreshDevices(names []string, selected string)               {}
func SetAutoPaste(bool)                                            {}
func OnAutoPaste(func(bool))                                       {}
func Quit()                                                        {}
