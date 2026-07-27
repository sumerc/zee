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
	// modelGroups records each group submenu and its [start,end) slice of
	// models, so a model-state change can re-derive the group's enabled state
	// and "(no API key)" suffix (a key added via Reload Config re-enables its
	// provider without rebuilding the menu).
	modelGroups []struct {
		item       *systray.MenuItem
		label      string
		start, end int
	}
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

// addSubmenuSeparator draws a divider inside a submenu. systray can only put a
// real separator in the top-level menu, so submenus fake one with a disabled
// item.
func addSubmenuSeparator(parent *systray.MenuItem) {
	item := parent.AddSubMenuItem("─────────", "")
	item.Disable()
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

	loginTitle, loginTip := "Start on Login", "Launch zee when you log in"
	if !loginAvailable {
		loginTitle, loginTip = "Start on Login (installed app only)", "Auto-start applies to Zee.app in /Applications, not to a dev build"
	}
	mLogin = mSettings.AddSubMenuItemCheckbox(loginTitle, loginTip, loginOn)
	if !loginAvailable {
		mLogin.Disable()
	}
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

	// Closes off the toggles, leaving the file editors below it.
	addSubmenuSeparator(mSettings)

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

	mEditSettings = mSettings.AddSubMenuItem("Edit Settings…", "Open config.json (apply with Reload Config)")
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

	// Reload Config acts on the three files above rather than being a fourth
	// editor, so it sits on its own side of a separator.
	addSubmenuSeparator(mSettings)

	mReload := mSettings.AddSubMenuItem("Reload Config", "Re-read config.json + credentials.json and apply the changes")
	mReload.Click(func() {
		if reloadCfgCb != nil {
			go reloadCfgCb()
		}
	})

	addSubmenuSeparator(mSettings)

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
	addSubmenuSeparator(mSettings)

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
			modelGroups = append(modelGroups, struct {
				item       *systray.MenuItem
				label      string
				start, end int
			}{provMenu, group, i, j})
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
			// Divide Local from the cloud providers.
			if group == "Local" && j < len(models) {
				addSubmenuSeparator(mBackend)
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
	refreshModelGroup(idx)
}

// refreshModelGroup re-derives the group submenu containing models[idx]:
// enabled iff any of its models is usable, with the "(no API key)" suffix
// tracking that state.
func refreshModelGroup(idx int) {
	for _, g := range modelGroups {
		if idx < g.start || idx >= g.end {
			continue
		}
		trayMu.Lock()
		anyUsable := false
		for k := g.start; k < g.end && k < len(models); k++ {
			if models[k].State != ModelUnavailable {
				anyUsable = true
			}
		}
		trayMu.Unlock()
		title := g.label
		if !anyUsable {
			title += " (no API key)"
			g.item.Disable()
		} else {
			g.item.Enable()
		}
		g.item.SetTitle(title)
		return
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
		trayMu.Lock()
		cb := langCb
		code := langEntries[idx].code
		trayMu.Unlock()
		// Ask before touching any state: a busy engine denies the change, and
		// the menu must keep showing the language the transcriber actually has
		// (macOS closes the menu on click, so doing nothing here means the old
		// checkmark is intact when it reopens).
		if cb != nil && !cb(code, true) {
			return
		}
		for _, e := range langEntries {
			e.item.Uncheck()
		}
		langEntries[idx].item.Check()
		trayMu.Lock()
		langCode = code
		langIntent = code // a user click is a real choice — remember it
		trayMu.Unlock()
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
