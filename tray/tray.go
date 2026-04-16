package tray

import (
	"fmt"
	"sync"
	"time"
	"zee/transcriber"
)

type Model struct {
	Provider      string // e.g. "groq", "openai", "deepgram"
	ProviderLabel string // e.g. "Groq"
	ModelID       string // e.g. "whisper-large-v3-turbo"
	Label         string // model display name
	HasKey        bool
	Active        bool
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

	loginOn bool
	loginCb func(bool) error

	modelMu  sync.Mutex
	models   []Model
	modelCb  func(provider, model string)

	isBTFn func(string) bool

	langCode string // current language code ("" = auto-detect)
	langCb   func(string)

	appVersion    string
	checkUpdateCb  func()
	saveAudioCb    func()
	editHintsCb    func()
)

var languages []transcriber.Language // set via SetLanguages

func OnCopyLast(fn func())            { copyLastFn = fn }
func OnRecord(start, stop func())     { recordFn = start; stopFn = stop }
func SetAutoPaste(on bool)            { autoPasteOn = on }
func OnAutoPaste(fn func(bool))       { autoPasteCb = fn }
func SetLogin(on bool)                { loginOn = on }
func OnLogin(fn func(bool) error)     { loginCb = fn }

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

func SetModels(m []Model, onSwitch func(provider, model string)) {
	modelMu.Lock()
	models = m
	modelCb = onSwitch
	modelMu.Unlock()
}

func SetLastRecording(dur time.Duration, totalMs float64) {
	updateCopyLastTitle(fmt.Sprintf("Copy Last Recorded Text (%.1fs | %dms)", dur.Seconds(), int(totalMs)))
}

func SetVersion(v string)     { appVersion = v }
func OnCheckUpdate(fn func()) { checkUpdateCb = fn }
func OnSaveAudio(fn func())  { saveAudioCb = fn }
func OnEditHints(fn func())  { editHintsCb = fn }

func SetLanguage(code string, onSwitch func(string)) {
	langCode = code
	langCb = onSwitch
}

func SetLanguages(langs []transcriber.Language) {
	languages = langs
	refreshLanguageMenu()
}

func SetBTCheck(fn func(string) bool) {
	isBTFn = fn
}

func statusText() string {
	modelMu.Lock()
	var provider, model string
	for _, m := range models {
		if m.Active {
			provider = m.ProviderLabel
			model = m.Label
			break
		}
	}
	modelMu.Unlock()
	lang := "Auto"
	if langCode != "" {
		lang = langCode
	}
	ver := ""
	if appVersion != "" && appVersion != "dev" {
		ver = " · " + appVersion
	}
	if provider == "" {
		return "𝘻𝘦𝘦"
	}
	return "𝘻𝘦𝘦 — " + provider + " · " + model + " · " + lang + ver
}

func updateStatus() {
	updateStatusItem(statusText())
}

func deviceDisplayName(name string) string {
	if isBTFn != nil && isBTFn(name) {
		return name + " [⚠ Lower audio quality]"
	}
	return name
}
