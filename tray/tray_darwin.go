//go:build darwin

package tray

import (
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

	mSettings     *systray.MenuItem
	mAutoPaste    *systray.MenuItem
	mLogin        *systray.MenuItem
	mHotkey       *systray.MenuItem
	mEditHints    *systray.MenuItem
	mEditSettings *systray.MenuItem
	mEditCreds    *systray.MenuItem
	mBackend      *systray.MenuItem
	mLanguage     *systray.MenuItem
	langEntries   []struct {
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

func updateRecordItem(rec bool) {
	if mRecord != nil {
		mRecord.SetTitle(recordTitle(rec))
	}
}

func updateAutoPasteItem(on bool) {
	if mAutoPaste == nil {
		return
	}
	if on {
		mAutoPaste.Check()
	} else {
		mAutoPaste.Uncheck()
	}
}

func updateLoginItem(on bool) {
	if mLogin == nil {
		return
	}
	if on {
		mLogin.Check()
	} else {
		mLogin.Uncheck()
	}
}

// updateHotkeyDisplay re-renders the two items that show the combo: the
// disabled "Hotkey: …" label and the Start/Stop Recording hint.
func updateHotkeyDisplay() {
	trayMu.Lock()
	label, rec := hotkeyLabel, recording
	trayMu.Unlock()
	if mHotkey != nil {
		mHotkey.SetTitle("Hotkey: " + label)
	}
	if mRecord != nil {
		mRecord.SetTitle(recordTitle(rec))
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

func updateTooltip(msg string) {
	systray.SetTooltip(msg)
}

func addDeviceItem(parent *systray.MenuItem, idx int, name string, checked bool) *systray.MenuItem {
	label := deviceDisplayName(name)
	item := parent.AddSubMenuItemCheckbox(label, label, checked)
	item.Click(func() {
		trayMu.Lock()
		currentName := ""
		if idx < len(deviceNames) {
			currentName = deviceNames[idx]
		}
		cb := deviceCb
		trayMu.Unlock()
		if cb != nil && currentName != "" {
			cb(currentName)
		}
		trayMu.Lock()
		if mDefaultDevice != nil {
			mDefaultDevice.Uncheck()
		}
		for _, it := range deviceItems {
			it.Uncheck()
		}
		if idx < len(deviceItems) {
			deviceItems[idx].Check()
		}
		trayMu.Unlock()
	})
	return item
}

func RefreshDevices(names []string, selected string) {
	if deviceReady == nil {
		return
	}
	<-deviceReady

	trayMu.Lock()
	defer trayMu.Unlock()

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
	systray.SetTemplateIcon(icon, icon)
	systray.SetTooltip("zee – push to talk")

	mStatus = systray.AddMenuItem(statusText(), "")
	mStatus.Disable()

	systray.AddSeparator()

	mRecord = systray.AddMenuItem(recordTitle(false), "Start or stop recording")
	mRecord.Click(func() {
		trayMu.Lock()
		rec := recording
		trayMu.Unlock()
		if rec {
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

	mSave := systray.AddMenuItem("Save Last Recording", "Save last audio + metadata to disk")
	mSave.Click(func() {
		if saveAudioCb != nil {
			go saveAudioCb()
		}
	})

	systray.AddSeparator()
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

	// systray can only put a real separator in the top-level menu, so submenus
	// draw their own from a disabled item (see the divider below Credentials).
	// This one closes off the toggles, leaving the file editors below it.
	sepToggles := mSettings.AddSubMenuItem("─────────", "")
	sepToggles.Disable()

	mEditHints = mSettings.AddSubMenuItem("Edit Hints…", "Edit vocabulary hints file")
	mEditHints.Click(func() {
		if editHintsCb != nil {
			go editHintsCb()
		}
	})
	trayMu.Lock()
	he := hintsEnabled
	trayMu.Unlock()
	if !he {
		mEditHints.Disable()
	}

	mEditSettings = mSettings.AddSubMenuItem("Edit Settings…", "Open config.json (changes apply live)")
	mEditSettings.Click(func() {
		if editSettingsCb != nil {
			go editSettingsCb()
		}
	})

	mEditCreds = mSettings.AddSubMenuItem("Edit Credentials…", "Open credentials.json to change provider API keys")
	mEditCreds.Click(func() {
		if editCredsCb != nil {
			go editCredsCb()
		}
	})

	sep := mSettings.AddSubMenuItem("─────────", "")
	sep.Disable()

	trayMu.Lock()
	hl := hotkeyLabel
	trayMu.Unlock()
	if hl != "" {
		mHotkey = mSettings.AddSubMenuItem("Hotkey: "+hl, "Change via Settings → Edit Settings…")
		mHotkey.Disable()
	}

	mDevices = mSettings.AddSubMenuItem("Microphone", "Select input device")

	trayMu.Lock()
	mDefaultDevice = mDevices.AddSubMenuItemCheckbox("System Default", "Use system default device", deviceSel == "")
	mDefaultDevice.Click(func() {
		trayMu.Lock()
		cb := deviceCb
		trayMu.Unlock()
		if cb != nil {
			cb("")
		}
		trayMu.Lock()
		for _, it := range deviceItems {
			it.Uncheck()
		}
		mDefaultDevice.Check()
		trayMu.Unlock()
	})
	deviceItems = make([]*systray.MenuItem, 0, len(deviceNames))
	for i, name := range deviceNames {
		item := addDeviceItem(mDevices, i, name, name == deviceSel)
		deviceItems = append(deviceItems, item)
	}
	trayMu.Unlock()

	// Divide the input device from the transcription pair (Model + Language).
	sepDevice := mSettings.AddSubMenuItem("─────────", "")
	sepDevice.Disable()

	trayMu.Lock()
	if len(models) > 0 {
		mBackend = mSettings.AddSubMenuItem("Model", "Select transcription model")
		modelItems = make([]*systray.MenuItem, len(models))
		// models are grouped by Group (contiguous); one submenu per group. Both
		// local engines share the "Local" group, each cloud provider is its own.
		for i := 0; i < len(models); {
			group := models[i].Group
			j, anyUsable := i, false
			for j < len(models) && models[j].Group == group {
				if models[j].State != ModelUnavailable {
					anyUsable = true
				}
				j++
			}
			label := group
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
					trayMu.Lock()
					mm := models[idx]
					cb := modelCb
					trayMu.Unlock()
					// Ready → switch; NeedsDownload → fetch. The handler (main)
					// dispatches and drives checkmarks via SetActiveModel.
					if cb == nil || (mm.State != ModelReady && mm.State != ModelNeedsDownload) {
						return
					}
					cb(mm.Provider, mm.ModelID)
				})
				modelItems[idx] = item
			}
			// Divide Local from the cloud providers (submenus can't hold real
			// separators, so a disabled item stands in — same trick as Settings).
			if group == "Local" && j < len(models) {
				sep := mBackend.AddSubMenuItem("─────────", "")
				sep.Disable()
			}
			i = j
		}
	}
	trayMu.Unlock()

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

	applyLanguage() // constrain the freshly-built menu to the active model

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
	trayMu.Lock()
	if idx < 0 || idx >= len(modelItems) || idx >= len(models) {
		trayMu.Unlock()
		return
	}
	m := models[idx]
	it := modelItems[idx]
	trayMu.Unlock()
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
	trayMu.Lock()
	checked := code == langCode
	trayMu.Unlock()
	item := mLanguage.AddSubMenuItemCheckbox(label, label, checked)
	item.Click(func() {
		// langEntries is built once in onReady and never mutated after, so it's
		// safe to read here without the lock; only langCode/langIntent need it.
		for _, e := range langEntries {
			e.item.Uncheck()
		}
		langEntries[idx].item.Check()
		trayMu.Lock()
		langCode = langEntries[idx].code
		langIntent = langCode // a user click is a real choice — remember it
		cb, code := langCb, langCode
		trayMu.Unlock()
		if cb != nil {
			cb(code, true)
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
	// Pure render: langCode was already derived (and the transcriber notified)
	// by applyLanguage in tray.go; this only shows the active model's set and
	// checks the effective language.
	trayMu.Lock()
	want := make(map[string]bool, len(languages))
	for _, l := range languages {
		want[l.Code] = true
	}
	code := langCode
	trayMu.Unlock()
	for _, e := range langEntries {
		if want[e.code] {
			e.item.Show()
			if e.code == code {
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
