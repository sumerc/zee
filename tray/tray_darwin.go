//go:build darwin

package tray

import (
	"os/exec"

	"github.com/energye/systray"
	"golang.design/x/hotkey/mainthread"
)

var (
	mRecord     *systray.MenuItem
	mCopy       *systray.MenuItem
	mDevices    *systray.MenuItem
	deviceItems []*systray.MenuItem
	deviceReady chan struct{}

	mSettings  *systray.MenuItem
	mAutoPaste *systray.MenuItem
	mLogin     *systray.MenuItem
	mBackend   *systray.MenuItem
	mLanguage  *systray.MenuItem
	langItems  []*systray.MenuItem
	mUpdate    *systray.MenuItem

	providerItems []*systray.MenuItem
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

func updateRecordingIcon(rec bool) {
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

func disableDevices() {
	if mDevices != nil {
		mDevices.Disable()
	}
}

func enableDevices() {
	if mDevices != nil {
		mDevices.Enable()
	}
}

func updateWarningIcon(on bool) {
	if on {
		systray.SetIcon(iconWarnHi)
	} else {
		systray.SetIcon(iconRecHi)
	}
}

func updateTooltip(msg string) {
	systray.SetTooltip(msg)
}

func addDeviceItem(parent *systray.MenuItem, idx int, name string, checked bool) *systray.MenuItem {
	label := deviceDisplayName(name)
	item := parent.AddSubMenuItemCheckbox(label, label, checked)
	item.Click(func() {
		deviceMu.Lock()
		// Use current name from deviceNames, not the captured name
		// (RefreshDevices may have changed the title)
		currentName := ""
		if idx < len(deviceNames) {
			currentName = deviceNames[idx]
		}
		cb := deviceCb
		deviceMu.Unlock()
		if cb != nil && currentName != "" {
			cb(currentName)
		}
		deviceMu.Lock()
		for _, it := range deviceItems {
			it.Uncheck()
		}
		if idx < len(deviceItems) {
			deviceItems[idx].Check()
		}
		deviceMu.Unlock()
	})
	return item
}

func RefreshDevices(names []string, selected string) {
	if deviceReady == nil {
		return
	}
	<-deviceReady

	deviceMu.Lock()
	defer deviceMu.Unlock()

	deviceNames = names
	deviceSel = selected

	for i, item := range deviceItems {
		if i < len(names) {
			label := deviceDisplayName(names[i])
			item.SetTitle(label)
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

	for i := len(deviceItems); i < len(names); i++ {
		item := addDeviceItem(mDevices, i, names[i], names[i] == selected)
		deviceItems = append(deviceItems, item)
	}
}

func onReady() {
	systray.SetTemplateIcon(iconIdleHi, iconIdle)
	systray.SetTooltip("zee – push to talk")

	mCopy = systray.AddMenuItem("Copy Last Recorded Text", "Copy last transcription to clipboard")
	mCopy.Disable()
	mCopy.Click(func() {
		if copyLastFn != nil {
			copyLastFn()
		}
	})

	systray.AddSeparator()

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

	mSettings = systray.AddMenuItem("Settings", "Settings")

	mDevices = mSettings.AddSubMenuItem("Devices", "Select input device")

	deviceMu.Lock()
	deviceItems = make([]*systray.MenuItem, 0, len(deviceNames))
	for i, name := range deviceNames {
		item := addDeviceItem(mDevices, i, name, name == deviceSel)
		deviceItems = append(deviceItems, item)
	}
	deviceMu.Unlock()
	mAutoPaste = mSettings.AddSubMenuItemCheckbox("Auto-paste", "Auto-paste transcribed text", autoPasteOn)
	mAutoPaste.Click(func() {
		if mAutoPaste.Checked() {
			mAutoPaste.Uncheck()
		} else {
			mAutoPaste.Check()
		}
		if autoPasteCb != nil {
			autoPasteCb(mAutoPaste.Checked())
		}
	})

	mLogin = mSettings.AddSubMenuItemCheckbox("Start on Login", "Launch zee when you log in", loginOn)
	mLogin.Click(func() {
		if mLogin.Checked() {
			mLogin.Uncheck()
		} else {
			mLogin.Check()
		}
		if loginCb != nil {
			loginCb(mLogin.Checked())
		}
	})

	providerMu.Lock()
	if len(providers) > 0 {
		mBackend = mSettings.AddSubMenuItem("Transcriber Backend", "Select transcription backend")
		providerItems = make([]*systray.MenuItem, 0, len(providers))
		for i, p := range providers {
			idx := i
			title := p.Label
			if !p.HasKey {
				title += " (no API key)"
			}
			item := mBackend.AddSubMenuItemCheckbox(title, title, p.Active)
			if !p.HasKey {
				item.Disable()
			}
			item.Click(func() {
				providerMu.Lock()
				pr := providers[idx]
				cb := providerCb
				providerMu.Unlock()
				if !pr.HasKey || cb == nil {
					return
				}
				cb(pr.Name)
				providerMu.Lock()
				for j, it := range providerItems {
					if j == idx {
						it.Check()
						providers[j].Active = true
					} else {
						it.Uncheck()
						providers[j].Active = false
					}
				}
				providerMu.Unlock()
			})
			providerItems = append(providerItems, item)
		}
	}
	providerMu.Unlock()

	mLanguage = mSettings.AddSubMenuItem("Language", "Select transcription language")
	langItems = make([]*systray.MenuItem, 0, len(Languages))
	for i, lang := range Languages {
		idx := i
		item := mLanguage.AddSubMenuItemCheckbox(lang.Label, lang.Label, lang.Code == langCode)
		item.Click(func() {
			for j, it := range langItems {
				if j == idx {
					it.Check()
				} else {
					it.Uncheck()
				}
			}
			if langCb != nil {
				langCb(Languages[idx].Code)
			}
		})
		langItems = append(langItems, item)
	}

	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit zee")
	mQuit.Click(func() { Quit() })
	systray.CreateMenu()

	close(deviceReady)
}

func updateCopyLastTitle(title string) {
	if mCopy != nil {
		mCopy.SetTitle(title)
		mCopy.Enable()
	}
}

func addUpdateMenuItem(version string) {
	if mUpdate != nil {
		mUpdate.SetTitle("⚠ Update available: " + version)
		mUpdate.Show()
		return
	}
	if mSettings == nil {
		return
	}
	mUpdate = mSettings.AddSubMenuItem("Update available: "+version, "Open release page")
	mUpdate.Click(func() {
		url := "https://github.com/sumerc/zee/releases/tag/" + version
		exec.Command("open", url).Start()
	})
}

func disableBackend() {
	if mBackend != nil {
		mBackend.Disable()
	}
}

func enableBackend() {
	if mBackend != nil {
		mBackend.Enable()
	}
}

func onExit() {
	closeOnce.Do(func() { close(quitCh) })
}
