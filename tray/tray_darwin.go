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

	deviceMu    sync.Mutex
	deviceNames []string
	deviceSel   string
	deviceCb    func(string)
	deviceItems []*systray.MenuItem
)

func Init() <-chan struct{} {
	start, _ := systray.RunWithExternalLoop(onReady, onExit)
	done := make(chan struct{})
	mainthread.Call(func() {
		start()
		close(done)
	})
	<-done
	return quitCh
}

func SetRecording(recording bool) {
	if recording {
		systray.SetIcon(iconRecHi)
	} else {
		systray.SetTemplateIcon(iconIdleHi, iconIdle)
	}
}

func OnCopyLast(fn func()) { copyLastFn = fn }

func Quit() {
	closeOnce.Do(func() { close(quitCh) })
}

// SetDevices must be called BEFORE Init. It configures the device list
// that will be rendered in the tray menu.
func SetDevices(names []string, selected string, onSwitch func(name string)) {
	deviceMu.Lock()
	deviceNames = names
	deviceSel = selected
	deviceCb = onSwitch
	deviceMu.Unlock()
}

func onReady() {
	systray.SetTemplateIcon(iconIdleHi, iconIdle)
	systray.SetTooltip("zee – push to talk")
	mCopy := systray.AddMenuItem("Copy Last", "Copy last transcription to clipboard")
	mCopy.Click(func() {
		if copyLastFn != nil {
			copyLastFn()
		}
	})

	deviceMu.Lock()
	if len(deviceNames) > 0 {
		mDevices := systray.AddMenuItem("Devices", "Select input device")
		deviceItems = make([]*systray.MenuItem, len(deviceNames))
		for i, name := range deviceNames {
			n := name
			idx := i
			deviceItems[i] = mDevices.AddSubMenuItemCheckbox(n, n, n == deviceSel)
			deviceItems[i].Click(func() {
				if deviceCb != nil {
					deviceCb(n)
				}
				for _, it := range deviceItems {
					it.Uncheck()
				}
				deviceItems[idx].Check()
			})
		}
	}
	deviceMu.Unlock()

	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit zee")
	mQuit.Click(func() { Quit() })
	systray.CreateMenu()
}

func onExit() {
	closeOnce.Do(func() { close(quitCh) })
}
