package beep

var disabled bool

func Disable() { disabled = true }

const (
	sampleRate = 44100

	// Start beep: high pitch, short
	startFreq   = 1200
	startVolume = 0.5
	startDecay  = 60

	// End beep: medium pitch, slightly longer
	endFreq   = 900
	endVolume = 0.5
	endDecay  = 40

	// Error beep: low pitch double-beep
	errorFreq   = 350
	errorVolume = 0.6
	errorDecay  = 30
)

// Platform-specific durations (darwin uses shorter durations)
