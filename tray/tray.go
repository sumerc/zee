package tray

import (
	"fmt"
	"sync"
	"time"
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
)

type Language struct {
	Code  string // ISO-639-1
	Label string
}

// Languages supported by both Groq (Whisper) and Deepgram Nova-2.
var Languages = []Language{
	{"", "Auto-detect"},
	{"bg", "Bulgarian"},
	{"ca", "Catalan"},
	{"zh", "Chinese"},
	{"cs", "Czech"},
	{"da", "Danish"},
	{"nl", "Dutch"},
	{"en", "English"},
	{"et", "Estonian"},
	{"fi", "Finnish"},
	{"fr", "French"},
	{"de", "German"},
	{"el", "Greek"},
	{"hi", "Hindi"},
	{"hu", "Hungarian"},
	{"id", "Indonesian"},
	{"it", "Italian"},
	{"ja", "Japanese"},
	{"ko", "Korean"},
	{"lv", "Latvian"},
	{"lt", "Lithuanian"},
	{"ms", "Malay"},
	{"no", "Norwegian"},
	{"pl", "Polish"},
	{"pt", "Portuguese"},
	{"ro", "Romanian"},
	{"ru", "Russian"},
	{"sk", "Slovak"},
	{"es", "Spanish"},
	{"sv", "Swedish"},
	{"th", "Thai"},
	{"tr", "Turkish"},
	{"uk", "Ukrainian"},
	{"vi", "Vietnamese"},
}

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

func SetUpdateAvailable(version string) {
	addUpdateMenuItem(version)
}

func SetLanguage(code string, onSwitch func(string)) {
	langCode = code
	langCb = onSwitch
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
