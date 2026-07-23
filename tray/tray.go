package tray

import (
	"fmt"
	"sync"
	"time"
	"zee/transcriber"
)

// ModelState drives how a model entry renders in the menu.
type ModelState int

const (
	ModelReady         ModelState = iota // selectable now
	ModelNeedsDownload                   // missing, user can fetch it (local)
	ModelDownloading                     // fetch in progress (shows %)
	ModelUnavailable                     // can't be used (e.g. cloud, no key)
)

type Model struct {
	Provider      string // e.g. "groq", "openai", "parakeet"
	ProviderLabel string // e.g. "Groq"
	ModelID       string // e.g. "whisper-large-v3-turbo"
	Label         string // model display name
	State         ModelState
	Detail        string // size when NeedsDownload, percent when Downloading
	Active        bool
}

var (
	quitCh    = make(chan struct{})
	closeOnce sync.Once

	copyLastFn func()
	recordFn   func()
	stopFn     func()

	// trayMu guards all mutable tray state below (recording/warning, the device
	// list, the model list, the language fields, the hints toggle). It is held
	// only around field reads/writes — never across a systray update or a
	// callback, both of which re-enter these accessors and would deadlock.
	trayMu sync.Mutex

	recording bool
	warning   bool

	deviceNames []string
	deviceSel   string
	deviceCb    func(string)

	autoPasteOn bool
	autoPasteCb func(bool)

	loginOn bool
	loginCb func(bool) error

	models  []Model
	modelCb func(provider, model string)

	isBTFn func(string) bool

	langCode   string // effective language shown for the active model ("" = auto-detect)
	langIntent string // user's persisted choice; survives models that can't offer it
	langCb     func(code string, persist bool)

	appVersion     string
	checkUpdateCb  func()
	saveAudioCb    func()
	editHintsCb    func()
	editSettingsCb func()
	editCredsCb    func()
	hotkeyLabel    string // display-only current push-to-talk combo (e.g. "⌃⇧Space")
)

var languages []transcriber.Language // set via SetLanguages

func OnCopyLast(fn func())        { copyLastFn = fn }
func OnRecord(start, stop func()) { recordFn = start; stopFn = stop }
func OnAutoPaste(fn func(bool))   { autoPasteCb = fn }
func OnLogin(fn func(bool) error) { loginCb = fn }

// SetAutoPaste / SetLogin set the checkbox state; before Init they seed the
// menu build, after Init (config-file reload) they re-render the item.
func SetAutoPaste(on bool) {
	trayMu.Lock()
	autoPasteOn = on
	trayMu.Unlock()
	updateAutoPasteItem(on)
}

func SetLogin(on bool) {
	trayMu.Lock()
	loginOn = on
	trayMu.Unlock()
	updateLoginItem(on)
}

func SetRecording(rec bool) {
	trayMu.Lock()
	recording = rec
	warning = false
	trayMu.Unlock()
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
	trayMu.Lock()
	if !recording {
		trayMu.Unlock()
		return
	}
	warning = on
	trayMu.Unlock()
	updateWarningIcon(on)
}

// SetTranscribing shows the "transcription in progress" icon (a blue status
// dot). The icon returns to idle on the next SetRecording(false).
func SetTranscribing(on bool) {
	updateTranscribingIcon(on)
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
	trayMu.Lock()
	deviceNames = names
	deviceSel = selected
	if onSwitch != nil {
		deviceCb = onSwitch
	}
	trayMu.Unlock()
}

func SetModels(m []Model, onSwitch func(provider, model string)) {
	trayMu.Lock()
	models = m
	modelCb = onSwitch
	trayMu.Unlock()
}

// UpdateModelState re-renders a single model entry (used while a local model
// downloads, and when it becomes ready). Safe to call from any goroutine.
func UpdateModelState(provider, modelID string, state ModelState, detail string) {
	trayMu.Lock()
	idx := -1
	for i := range models {
		if models[i].Provider == provider && models[i].ModelID == modelID {
			models[i].State = state
			models[i].Detail = detail
			idx = i
			break
		}
	}
	trayMu.Unlock()
	if idx >= 0 {
		updateModelItem(idx)
	}
}

// SetActiveModel marks one model active (checked) and the rest inactive,
// re-rendering only the entries that changed. Called by the app after a
// successful model switch.
func SetActiveModel(provider, modelID string) {
	trayMu.Lock()
	var changed []int
	for i := range models {
		want := models[i].Provider == provider && models[i].ModelID == modelID
		if models[i].Active != want {
			models[i].Active = want
			changed = append(changed, i)
		}
	}
	trayMu.Unlock()
	for _, i := range changed {
		updateModelItem(i)
	}
	updateStatus()
}

// modelTitle is the menu label for a model given its state.
func modelTitle(m Model) string {
	switch m.State {
	case ModelNeedsDownload:
		if m.Detail != "" {
			return m.Label + " — download " + m.Detail
		}
		return m.Label + " — download"
	case ModelDownloading:
		if m.Detail != "" {
			return m.Label + " — downloading " + m.Detail
		}
		return m.Label + " — downloading…"
	default:
		return m.Label
	}
}

func SetLastRecording(dur time.Duration, totalMs float64) {
	updateCopyLastTitle(fmt.Sprintf("Copy Last Recorded Text (%.1fs | %dms)", dur.Seconds(), int(totalMs)))
}

// hintsEnabled gates the "Edit Hints…" item: local providers ignore hints
// (greedy decode has no biasing), so the item is greyed out when local is active.
var hintsEnabled = true

// SetHintsEnabled greys out / restores the "Edit Hints…" menu item. Safe to
// call before Init (the state is applied when the menu is built).
func SetHintsEnabled(on bool) {
	trayMu.Lock()
	hintsEnabled = on
	trayMu.Unlock()
	setHintsEnabled(on)
}

func SetVersion(v string)         { appVersion = v }
func OnCheckUpdate(fn func())     { checkUpdateCb = fn }
func OnSaveAudio(fn func())       { saveAudioCb = fn }
func OnEditHints(fn func())       { editHintsCb = fn }
func OnEditSettings(fn func())    { editSettingsCb = fn }
func OnEditCredentials(fn func()) { editCredsCb = fn }

// SetHotkeyLabel sets the display-only current push-to-talk combo shown (and
// disabled) in the menu, and the combo hint on the Start/Stop Recording item.
// Before Init it seeds the menu build; after Init (config-file reload) it
// re-renders both items.
func SetHotkeyLabel(label string) {
	trayMu.Lock()
	hotkeyLabel = label
	trayMu.Unlock()
	updateHotkeyDisplay()
}

// recordTitle is the Start/Stop Recording menu label, with the current
// push-to-talk combo as a hint.
func recordTitle(rec bool) string {
	trayMu.Lock()
	label := hotkeyLabel
	trayMu.Unlock()
	title := "○ Start Recording"
	if rec {
		title = "● Stop Recording"
	}
	if label != "" {
		title += " (" + label + ")"
	}
	return title
}

// SelectLanguage changes the user's language intent from outside the menu
// (config-file reload) and re-renders the checkmarks; the effective language
// reaches the transcriber through the usual callback (persist=false — the
// value came from the file, writing it back would be an echo).
func SelectLanguage(code string) {
	trayMu.Lock()
	same := langIntent == code
	langIntent = code
	trayMu.Unlock()
	if !same {
		applyLanguage()
	}
}

// applyLanguage derives the effective language from the user's intent (the
// fallback when the model can't offer the intent is applied to the transcriber
// but never persisted, so switching back to a capable model restores the
// intent), delivers it to the transcriber callback, and re-renders the
// platform menu. Platform-neutral so config-file edits work without a menu.
func applyLanguage() {
	trayMu.Lock()
	langCode = effectiveLang(langIntent, languages)
	cb, code := langCb, langCode
	trayMu.Unlock()
	if cb != nil {
		cb(code, false)
	}
	refreshLanguageMenu()
}

func SetLanguage(code string, onSwitch func(code string, persist bool)) {
	trayMu.Lock()
	langCode = code
	langIntent = code
	langCb = onSwitch
	trayMu.Unlock()
}

func SetLanguages(langs []transcriber.Language) {
	trayMu.Lock()
	languages = langs
	trayMu.Unlock()
	applyLanguage()
}

// effectiveLang picks the language to actually use for a model that offers
// `langs`, given the user's intended choice. Intent wins when the model can
// offer it; otherwise it falls back to the model's first language (Auto-detect
// for multilingual models, English for English-only ones). The intent itself is
// never mutated here, so switching back to a capable model restores it.
func effectiveLang(intent string, langs []transcriber.Language) string {
	for _, l := range langs {
		if l.Code == intent {
			return intent
		}
	}
	if len(langs) > 0 {
		return langs[0].Code
	}
	return ""
}

func SetBTCheck(fn func(string) bool) {
	isBTFn = fn
}

func statusText() string {
	trayMu.Lock()
	var provider, model string
	for _, m := range models {
		if m.Active {
			provider = m.ProviderLabel
			model = m.Label
			break
		}
	}
	lang := "Auto"
	if langCode != "" {
		lang = langCode
	}
	ver := ""
	if appVersion != "" && appVersion != "dev" {
		ver = " · " + appVersion
	}
	trayMu.Unlock()
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
