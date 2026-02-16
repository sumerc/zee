//go:build darwin

package tray

import (
	"sync"

	"github.com/energye/systray"
	"golang.design/x/hotkey/mainthread"
)

var (
	quitCh     = make(chan struct{})
	closeOnce  sync.Once
	copyLastFn func()
	recordFn   func()
	stopFn     func()

	mRecord       *systray.MenuItem
	recording     bool

	deviceMu      sync.Mutex
	deviceNames   []string
	deviceSel     string
	deviceCb      func(string)
	mDevices      *systray.MenuItem
	deviceItems   []*systray.MenuItem
	deviceReady   chan struct{}
)

func Init() <-chan struct{} {
	deviceReady = make(chan struct{})
	start, _ := systray.RunWithExternalLoop(onReady, onExit)
	done := make(chan struct{})
	mainthread.Call(func() {
		start()
		close(done)
	})
	<-done
	return quitCh
}

func SetRecording(rec bool) {
	recording = rec
	if rec {
		systray.SetIcon(iconRecHi)
		if mRecord != nil {
			mRecord.SetTitle("Stop Recording")
		}
	} else {
		systray.SetTemplateIcon(iconIdleHi, iconIdle)
		if mRecord != nil {
			mRecord.SetTitle("Start Recording")
		}
	}
}

func OnCopyLast(fn func()) { copyLastFn = fn }
func OnRecord(start, stop func()) { recordFn = start; stopFn = stop }

func Quit() {
	closeOnce.Do(func() { close(quitCh) })
}

// SetDevices configures the initial device list. Call before Init for
// initial population, or after Init to update dynamically.
func SetDevices(names []string, selected string, onSwitch func(name string)) {
	deviceMu.Lock()
	deviceNames = names
	deviceSel = selected
	if onSwitch != nil {
		deviceCb = onSwitch
	}
	deviceMu.Unlock()
}

// RefreshDevices updates the tray device submenu at runtime.
// Must be called after Init.
func RefreshDevices(names []string, selected string) {
	if deviceReady == nil {
		return
	}
	<-deviceReady

	deviceMu.Lock()
	defer deviceMu.Unlock()

	deviceNames = names
	deviceSel = selected

	// reuse existing slots, hide extras
	for i, item := range deviceItems {
		if i < len(names) {
			item.SetTitle(names[i])
			item.SetTooltip(names[i])
			item.Show()
			if names[i] == selected {
				item.Check()
			} else {
				item.Uncheck()
			}
		} else {
			item.Hide()
			item.Uncheck()
		}
	}

	// add new slots if device list grew
	for i := len(deviceItems); i < len(names); i++ {
		n := names[i]
		idx := i
		item := mDevices.AddSubMenuItemCheckbox(n, n, n == selected)
		item.Click(func() {
			deviceMu.Lock()
			cb := deviceCb
			deviceMu.Unlock()
			if cb != nil {
				cb(n)
			}
			deviceMu.Lock()
			for _, it := range deviceItems {
				it.Uncheck()
			}
			deviceItems[idx].Check()
			deviceMu.Unlock()
		})
		deviceItems = append(deviceItems, item)
	}
}

func onReady() {
	systray.SetTemplateIcon(iconIdleHi, iconIdle)
	systray.SetTooltip("zee – push to talk")

	mRecord = systray.AddMenuItem("Start Recording", "Start or stop recording")
	mRecord.Click(func() {
		if recording {
			if stopFn != nil {
				stopFn()
			}
		} else {
			if recordFn != nil {
				recordFn()
			}
		}
	})

	mCopy := systray.AddMenuItem("Copy Last Recording", "Copy last transcription to clipboard")
	mCopy.Click(func() {
		if copyLastFn != nil {
			copyLastFn()
		}
	})

	mDevices = systray.AddMenuItem("Devices", "Select input device")

	deviceMu.Lock()
	deviceItems = make([]*systray.MenuItem, 0, len(deviceNames))
	for i, name := range deviceNames {
		n := name
		idx := i
		item := mDevices.AddSubMenuItemCheckbox(n, n, n == deviceSel)
		item.Click(func() {
			deviceMu.Lock()
			cb := deviceCb
			deviceMu.Unlock()
			if cb != nil {
				cb(n)
			}
			deviceMu.Lock()
			for _, it := range deviceItems {
				it.Uncheck()
			}
			deviceItems[idx].Check()
			deviceMu.Unlock()
		})
		deviceItems = append(deviceItems, item)
	}
	deviceMu.Unlock()

	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit zee")
	mQuit.Click(func() { Quit() })
	systray.CreateMenu()

	close(deviceReady)
}

func onExit() {
	closeOnce.Do(func() { close(quitCh) })
}
