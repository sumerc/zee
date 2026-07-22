//go:build darwin

package tray

import (
	"os/exec"
	"strings"

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

// darkMenuBar caches the menu bar appearance, read once at startup
// (detectMenuBarAppearance). Reading it costs a `defaults` subprocess fork,
// which is cheap at launch but very expensive once a multi-GB local model is
// resident (fork is copy-on-write over the whole process) — so it must never
// run on the icon hot path, where it would stall the run loop and delay hotkey
// events. A mid-session light/dark flip is picked up on the next relaunch.
var darkMenuBar bool

func detectMenuBarAppearance() {
	out, _ := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle").Output()
	darkMenuBar = strings.TrimSpace(string(out)) == "Dark" // key is absent in light mode
}

// themedIcon picks the colored-state variant for the current menu bar
// appearance: Hi (white glyph) on a dark bar, Lo (dark glyph) on a light one.
// The colored icons can't be templates (template mode discards the state dot's
// color), so the variant is chosen from the cached appearance.
func themedIcon(hi, lo []byte) []byte {
	if darkMenuBar {
		return hi
	}
	return lo
}

func updateRecordingIcon(rec bool) {
	if rec {
		systray.SetIcon(themedIcon(iconRecHi, iconRecLo))
	} else {
		systray.SetTemplateIcon(iconIdleHi, iconIdleHi)
	}
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

func updateWarningIcon(on bool) {
	if on {
		systray.SetIcon(themedIcon(iconWarnHi, iconWarnLo))
	} else {
		systray.SetIcon(themedIcon(iconRecHi, iconRecLo))
	}
}

func updateTranscribingIcon(on bool) {
	if on {
		systray.SetIcon(themedIcon(iconBusyHi, iconBusyLo))
	} else {
		systray.SetTemplateIcon(iconIdleHi, iconIdleHi)
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
	detectMenuBarAppearance() // once, before a big model is resident (see darkMenuBar)
	systray.SetTemplateIcon(iconIdleHi, iconIdleHi)
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

	trayMu.Lock()
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
