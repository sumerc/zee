//go:build !darwin

package tray

func Init() <-chan struct{}                              { return make(chan struct{}) }
func RefreshDevices(names []string, selected string)     {}
func updateRecordingIcon(bool)                           {}
func updateWarningIcon(bool)                             {}
func updateTooltip(string)                               {}
func updateCopyLastTitle(string)                         {}
func addUpdateMenuItem(string)                           {}
func disableDevices()                                    {}
func enableDevices()                                     {}
func disableBackend()                                    {}
func enableBackend()                                     {}
