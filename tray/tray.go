package tray

import (
	"fmt"
	"sync"
	"time"
)

type Provider struct {
	Name   string
	Label  string
	HasKey bool
	Active bool
}

var (
	quitCh    = make(chan struct{})
	closeOnce sync.Once

	copyLastFn func()
	recordFn   func()
	stopFn     func()

	recording bool
	warning   bool

	deviceMu    sync.Mutex
	deviceNames []string
	deviceSel   string
	deviceCb    func(string)

	autoPasteOn bool
	autoPasteCb func(bool)

	providerMu    sync.Mutex
	providers     []Provider
	providerCb    func(string)

	isBTFn func(string) bool
)

func OnCopyLast(fn func())            { copyLastFn = fn }
func OnRecord(start, stop func())     { recordFn = start; stopFn = stop }
func SetAutoPaste(on bool)            { autoPasteOn = on }
func OnAutoPaste(fn func(bool))       { autoPasteCb = fn }

func SetRecording(rec bool) {
	recording = rec
	warning = false
	updateRecordingIcon(rec)
	if rec {
		disableDevices()
		disableBackend()
	} else {
		enableDevices()
		enableBackend()
	}
}

func SetWarning(on bool) {
	if !recording {
		return
	}
	warning = on
	updateWarningIcon(on)
}

func SetError(msg string) {
	updateTooltip("zee – " + msg)
	go func() {
		time.Sleep(10 * time.Second)
		updateTooltip("zee – push to talk")
	}()
}

func Quit() {
	closeOnce.Do(func() { close(quitCh) })
}

func SetDevices(names []string, selected string, onSwitch func(name string)) {
	deviceMu.Lock()
	deviceNames = names
	deviceSel = selected
	if onSwitch != nil {
		deviceCb = onSwitch
	}
	deviceMu.Unlock()
}

func SetProviders(p []Provider, onSwitch func(string)) {
	providerMu.Lock()
	providers = p
	providerCb = onSwitch
	providerMu.Unlock()
}

func SetLastRecording(dur time.Duration) {
	updateCopyLastTitle(fmt.Sprintf("Copy Last Recorded Text (%.1fs)", dur.Seconds()))
}

func SetUpdateAvailable(version string) {
	addUpdateMenuItem(version)
}

func SetBTCheck(fn func(string) bool) {
	isBTFn = fn
}

func deviceDisplayName(name string) string {
	if isBTFn != nil && isBTFn(name) {
		return name + " [⚠ Lower audio quality]"
	}
	return name
}
