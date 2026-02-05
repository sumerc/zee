package main

// EventSink abstracts the display layer so both the Bubble Tea TUI
// and the Wails GUI can receive the same recording/transcription events.
type EventSink interface {
	RecordingStart()
	RecordingStop()
	RecordingTick(duration float64)
	AudioLevel(level float64)
	NoVoiceWarning()
	Transcription(text string, metrics []string, copied bool, noSpeech bool)
	ModeLine(text string)
	DeviceLine(text string)
	RateLimit(text string)
}
