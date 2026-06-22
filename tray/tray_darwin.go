//go:build darwin

package tray

import (
	"os"

	"github.com/energye/systray"
	"golang.design/x/hotkey/mainthread"

	"zee/transcriber"
)

var (
	mStatus        *systray.MenuItem
	mRecord        *systray.MenuItem
	mCopy          *systray.MenuItem
	mDevices       *systray.MenuItem
	mDefaultDevice *systray.MenuItem
	deviceItems    []*systray.MenuItem
	deviceReady    chan struct{}

	mSettings   *systray.MenuItem
	mAutoPaste  *systray.MenuItem
	mLogin      *systray.MenuItem
	mEditHints  *systray.MenuItem
	mBackend    *systray.MenuItem
	mLanguage   *systray.MenuItem
	langEntries []struct {
		item *systray.MenuItem
		code string
	}
	mCheckUpdate *systray.MenuItem

	modelItems []*systray.MenuItem
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
			mRecord.SetTitle("● Stop Recording (Shift+Control+Space)")
		}
	} else {
		systray.SetTemplateIcon(iconIdleHi, iconIdleHi)
		if mRecord != nil {
			mRecord.SetTitle("○ Start Recording (Shift+Control+Space)")
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
		if mDefaultDevice != nil {
			mDefaultDevice.Uncheck()
		}
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

	if mDefaultDevice != nil {
		if selected == "" {
			mDefaultDevice.Check()
		} else {
			mDefaultDevice.Uncheck()
		}
	}

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
	systray.SetTemplateIcon(iconIdleHi, iconIdleHi)
	systray.SetTooltip("zee – push to talk")

	mStatus = systray.AddMenuItem(statusText(), "")
	mStatus.Disable()

	systray.AddSeparator()

	mRecord = systray.AddMenuItem("○ Start Recording (Shift+Control+Space)", "Start or stop recording")
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

	mCopy = systray.AddMenuItem("Copy Last Recorded Text", "Copy last transcription to clipboard")
	mCopy.Disable()
	mCopy.Click(func() {
		if copyLastFn != nil {
			copyLastFn()
		}
	})

	if os.Getenv("ZEE_SAVE_LAST_AUDIO") != "" {
		mSave := systray.AddMenuItem("Save Last Recording", "Save last audio + metadata to disk")
		mSave.Click(func() {
			if saveAudioCb != nil {
				go saveAudioCb()
			}
		})
	}

	mSettings = systray.AddMenuItem("Settings", "Settings")

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
		want := !mLogin.Checked()
		if loginCb != nil {
			if err := loginCb(want); err != nil {
				return
			}
		}
		if want {
			mLogin.Check()
		} else {
			mLogin.Uncheck()
		}
	})

	mEditHints = mSettings.AddSubMenuItem("Edit Hints…", "Edit vocabulary hints file")
	mEditHints.Click(func() {
		if editHintsCb != nil {
			go editHintsCb()
		}
	})
	if !hintsEnabled {
		mEditHints.Disable()
	}

	sep := mSettings.AddSubMenuItem("─────────", "")
	sep.Disable()

	mDevices = mSettings.AddSubMenuItem("Microphone", "Select input device")

	deviceMu.Lock()
	mDefaultDevice = mDevices.AddSubMenuItemCheckbox("System Default", "Use system default device", deviceSel == "")
	mDefaultDevice.Click(func() {
		deviceMu.Lock()
		cb := deviceCb
		deviceMu.Unlock()
		if cb != nil {
			cb("")
		}
		deviceMu.Lock()
		for _, it := range deviceItems {
			it.Uncheck()
		}
		mDefaultDevice.Check()
		deviceMu.Unlock()
	})
	deviceItems = make([]*systray.MenuItem, 0, len(deviceNames))
	for i, name := range deviceNames {
		item := addDeviceItem(mDevices, i, name, name == deviceSel)
		deviceItems = append(deviceItems, item)
	}
	deviceMu.Unlock()

	modelMu.Lock()
	if len(models) > 0 {
		mBackend = mSettings.AddSubMenuItem("Model", "Select transcription model")
		modelItems = make([]*systray.MenuItem, len(models))
		// models are grouped by provider (contiguous); one submenu per provider.
		for i := 0; i < len(models); {
			prov := models[i].Provider
			j, anyUsable := i, false
			for j < len(models) && models[j].Provider == prov {
				if models[j].State != ModelUnavailable {
					anyUsable = true
				}
				j++
			}
			label := models[i].ProviderLabel
			if !anyUsable {
				label += " (no API key)"
			}
			provMenu := mBackend.AddSubMenuItem(label, label)
			if !anyUsable {
				provMenu.Disable()
			}
			for k := i; k < j; k++ {
				idx := k
				m := models[k]
				item := provMenu.AddSubMenuItemCheckbox(modelTitle(m), m.Label, m.Active && m.State == ModelReady)
				if m.State == ModelUnavailable || m.State == ModelDownloading {
					item.Disable()
				}
				item.Click(func() {
					modelMu.Lock()
					mm := models[idx]
					cb := modelCb
					modelMu.Unlock()
					// Ready → switch; NeedsDownload → fetch. The handler (main)
					// dispatches and drives checkmarks via SetActiveModel.
					if cb == nil || (mm.State != ModelReady && mm.State != ModelNeedsDownload) {
						return
					}
					cb(mm.Provider, mm.ModelID)
				})
				modelItems[idx] = item
			}
			i = j
		}
	}
	modelMu.Unlock()

	// Build a fixed item per known language (systray can't add items after
	// CreateMenu). refreshLanguageMenu then shows only the active model's set.
	mLanguage = mSettings.AddSubMenuItem("Language", "Select transcription language")
	for _, lang := range transcriber.AllLanguages() {
		addLangEntry(lang.Code, lang.Label)
	}

	systray.AddSeparator()

	mCheckUpdate = systray.AddMenuItem("Check for Updates…", "Check for updates")
	mCheckUpdate.Click(func() {
		if checkUpdateCb != nil {
			checkUpdateCb()
		}
	})

	mQuit := systray.AddMenuItem("Quit", "Quit zee")
	mQuit.Click(func() { Quit() })
	systray.CreateMenu()

	refreshLanguageMenu() // constrain the freshly-built menu to the active model

	close(deviceReady)
}

func updateCopyLastTitle(title string) {
	if mCopy != nil {
		mCopy.SetTitle(title)
		mCopy.Enable()
	}
}

// updateModelItem re-renders one model entry (title, checkmark, enabled) from
// its current state. Called on download progress and on model switch.
func updateModelItem(idx int) {
	modelMu.Lock()
	if idx < 0 || idx >= len(modelItems) || idx >= len(models) {
		modelMu.Unlock()
		return
	}
	m := models[idx]
	it := modelItems[idx]
	modelMu.Unlock()
	if it == nil {
		return
	}
	it.SetTitle(modelTitle(m))
	if m.Active && m.State == ModelReady {
		it.Check()
	} else {
		it.Uncheck()
	}
	if m.State == ModelReady || m.State == ModelNeedsDownload {
		it.Enable()
	} else {
		it.Disable()
	}
}

func addLangEntry(code, label string) {
	idx := len(langEntries)
	item := mLanguage.AddSubMenuItemCheckbox(label, label, code == langCode)
	item.Click(func() {
		for _, e := range langEntries {
			e.item.Uncheck()
		}
		langEntries[idx].item.Check()
		langCode = langEntries[idx].code
		if langCb != nil {
			langCb(langCode)
		}
		updateStatus()
	})
	langEntries = append(langEntries, struct {
		item *systray.MenuItem
		code string
	}{item, code})
}

func refreshLanguageMenu() {
	if mLanguage == nil {
		return
	}
	want := make(map[string]bool, len(languages))
	for _, l := range languages {
		want[l.Code] = true
	}
	// If the active model no longer offers the current language, fall back to
	// its first offered one (Auto-detect for multilingual models, English for
	// the English-only ones).
	if !want[langCode] {
		langCode = ""
		if len(languages) > 0 {
			langCode = languages[0].Code
		}
		if langCb != nil {
			langCb(langCode)
		}
	}
	for _, e := range langEntries {
		if want[e.code] {
			e.item.Show()
			if e.code == langCode {
				e.item.Check()
			} else {
				e.item.Uncheck()
			}
		} else {
			e.item.Hide()
			e.item.Uncheck()
		}
	}
	updateStatus()
}

func updateStatusItem(text string) {
	if mStatus != nil {
		mStatus.SetTitle(text)
	}
}

func setHintsEnabled(on bool) {
	if mEditHints == nil {
		return
	}
	if on {
		mEditHints.Enable()
	} else {
		mEditHints.Disable()
	}
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
