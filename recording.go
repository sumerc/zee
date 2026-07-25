package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"zee/audio"
	"zee/encoder"
	"zee/log"
	"zee/overlay"
	"zee/transcriber"
	"zee/tray"
)

// meterInterval throttles the overlay's level meter. The capture callback
// fires far more often than the meter can show, and pushing every frame just
// scrolls the bars into a blur.
const meterInterval = 33 * time.Millisecond

type recordingSession struct {
	capture         audio.CaptureDevice
	transcriberSess transcriber.Session
	stop            <-chan struct{}
	vp              *vadProcessor
	mon             *silenceMonitor
	tailWait        time.Duration // mic stays open this long after release (anti-clip)

	mu          sync.Mutex
	totalFrames uint64
	meterLevel  float32
	lastMeter   time.Time
	band        voiceBand
	stats       meterStats
	stopped     bool
	autoClosed  atomic.Bool
	// releasedAt (unix nanos) is when the user stopped talking — hotkey release,
	// or the silence auto-close, whichever ended the recording. It is the start
	// of the latency the user actually feels, so everything after it (tail wait,
	// device stop, encode, inference, paste) is measured from here. Atomic
	// because awaitStop and monitorSilence can reach the stop path concurrently.
	releasedAt atomic.Int64
	done       chan struct{}
	closeOnce  sync.Once
}

func newRecordingSession(capture audio.CaptureDevice, stop <-chan struct{}, sess transcriber.Session, silenceClose *atomic.Bool, tailWait time.Duration) (*recordingSession, error) {
	vp, err := newVADProcessor()
	if err != nil {
		return nil, fmt.Errorf("VAD init: %w", err)
	}
	return &recordingSession{
		capture:         capture,
		transcriberSess: sess,
		stop:            stop,
		vp:              vp,
		mon:             newSilenceMonitor(silenceClose),
		band:            newVoiceBand(encoder.SampleRate),
		tailWait:        tailWait,
		done:            make(chan struct{}),
	}, nil
}

func (r *recordingSession) onAudio(data []byte, frameCount uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	r.totalFrames += uint64(frameCount)

	if len(data) > 0 {
		r.transcriberSess.Feed(data)
		r.vp.Process(data)
		if now := time.Now(); now.Sub(r.lastMeter) >= meterInterval {
			r.lastMeter = now
			db := r.band.levelDB(data)
			speaking := r.vp.SpeakingNow(meterGateHold)
			r.stats.add(db, speaking)

			// The VAD opens the gate, the level sets the height. Feeding the
			// average a flat zero while it is shut is all the release the bars
			// need — they fade instead of cutting.
			var target float32
			if speaking {
				target = meterLevelAt(db)
			}
			r.meterLevel = r.meterLevel*(1-meterSmooth) + target*meterSmooth
			overlay.PushLevel(r.meterLevel)
		}
	}
}

// Meter response: a fixed dB window, a mild curve to lift the quiet end, and
// an EMA to take the jitter out.
//
// Fixed is not a compromise here, it is a requirement. The bars are a
// scrolling history, so the thirty already on screen were drawn to whatever
// scale was in force when they arrived. Adapt the gain and they are all
// suddenly wrong — which reads as the meter swelling and collapsing on its
// own. Every bar has to share one scale, so the scale cannot move.
const (
	meterFloorDB = -58.0 // bottom of the bars
	meterCeilDB  = -34.0 // full scale, with headroom above measured speech peaks
	meterCurve   = 0.9   // a touch of lift at the quiet end; 1.0 is a straight line
	meterSmooth  = 0.4   // weight of the newest reading in the running average

	// The meter listens to a voice band, not to everything the mic hears.
	// Measured on a saved recording: below 100 Hz — mains hum, USB noise, desk
	// rumble — sat 25 dB above every other noise band and did not move when
	// speaking, so it swamped a broadband reading. It was half the energy
	// during speech, which is what pinned the bars into a flat wall. Above
	// 4 kHz is mostly hiss: that band scored barely 2 dB of speech-to-noise,
	// against 15-20 dB for every band in between. Either edge can be switched
	// off with a 0.
	meterHighPassHz = 300.0  // 300 beat both 200 and 400 on the same recording
	meterLowPassHz  = 4000.0 // above this the mic hears hiss, not voice

	// meterGateHold is how long the meter keeps showing level after the VAD
	// last heard speech. Long enough to carry across the stops and consonants
	// inside a phrase, short enough that the bars fall away once you stop.
	meterGateHold = 250 * time.Millisecond
)

// biquad is one second-order Butterworth section, transposed direct form II.
// A zero or out-of-range cutoff yields the identity filter, so a band edge can
// be switched off without the caller branching around it.
type biquad struct {
	b0, b1, b2, a1, a2 float64
	z1, z2             float64
}

func newHighPass(cutoffHz, sampleRate float64) biquad {
	if cutoffHz <= 0 {
		return biquad{b0: 1}
	}
	cw, alpha := biquadShape(cutoffHz, sampleRate)
	a0 := 1 + alpha
	return biquad{
		b0: (1 + cw) / 2 / a0,
		b1: -(1 + cw) / a0,
		b2: (1 + cw) / 2 / a0,
		a1: -2 * cw / a0,
		a2: (1 - alpha) / a0,
	}
}

func newLowPass(cutoffHz, sampleRate float64) biquad {
	if cutoffHz <= 0 || cutoffHz >= sampleRate/2 {
		return biquad{b0: 1}
	}
	cw, alpha := biquadShape(cutoffHz, sampleRate)
	a0 := 1 + alpha
	return biquad{
		b0: (1 - cw) / 2 / a0,
		b1: (1 - cw) / a0,
		b2: (1 - cw) / 2 / a0,
		a1: -2 * cw / a0,
		a2: (1 - alpha) / a0,
	}
}

// biquadShape returns cos(w0) and the alpha for Q = 1/√2, the Butterworth
// value — the flattest passband, which is what a meter wants.
func biquadShape(cutoffHz, sampleRate float64) (cw, alpha float64) {
	w0 := 2 * math.Pi * cutoffHz / sampleRate
	return math.Cos(w0), math.Sin(w0) / math.Sqrt2
}

func (f *biquad) step(x float64) float64 {
	y := f.b0*x + f.z1
	f.z1 = f.b1*x - f.a1*y + f.z2
	f.z2 = f.b2*x - f.a2*y
	return y
}

// voiceBand is what the meter listens through: everything outside the range a
// voice occupies is noise as far as the bars are concerned, and on this mic it
// was most of the signal. It filters the meter's copy of the audio only — the
// transcriber is fed the untouched stream.
type voiceBand struct{ hp, lp biquad }

func newVoiceBand(sampleRate float64) voiceBand {
	return voiceBand{
		hp: newHighPass(meterHighPassHz, sampleRate),
		lp: newLowPass(meterLowPassHz, sampleRate),
	}
}

// levelDB filters a chunk of S16LE PCM and reports its RMS in dBFS. Filtering
// and measurement share one pass; the filter state carries across chunks.
func (v *voiceBand) levelDB(pcm []byte) float64 {
	n := len(pcm) / 2
	if n == 0 {
		return math.Inf(-1)
	}
	var sum float64
	for i := 0; i+1 < len(pcm); i += 2 {
		x := float64(int16(binary.LittleEndian.Uint16(pcm[i:]))) / 32768
		y := v.lp.step(v.hp.step(x))
		sum += y * y
	}
	rms := math.Sqrt(sum / float64(n))
	if rms <= 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(rms)
}

// meterLevelAt places a dBFS reading on the overlay's 0..1 meter.
func meterLevelAt(db float64) float32 {
	norm := (db - meterFloorDB) / (meterCeilDB - meterFloorDB)
	if norm <= 0 {
		return 0
	}
	if norm > 1 {
		norm = 1
	}
	return float32(math.Pow(norm, meterCurve))
}

// meterStats records what the mic delivered during a recording, split by
// whether the VAD heard speech at the time. The distance between the two
// averages is the signal-to-noise ratio the meter has to work with, and it is
// the number that decides what to do next: a wide gap means the window just
// needs setting, a narrow one means no window can help and the signal itself
// has to be cleaned up.
type meterStats struct{ speech, quiet dbRange }

type dbRange struct {
	n            int
	sumDB        float64
	minDB, maxDB float64
}

func (r *dbRange) add(db float64) {
	if r.n == 0 || db < r.minDB {
		r.minDB = db
	}
	if r.n == 0 || db > r.maxDB {
		r.maxDB = db
	}
	r.sumDB += db
	r.n++
}

func (r *dbRange) avg() float64 { return r.sumDB / float64(r.n) }

func (r *dbRange) String() string {
	if r.n == 0 {
		return "none"
	}
	return fmt.Sprintf("min=%.1f avg=%.1f max=%.1f n=%d", r.minDB, r.avg(), r.maxDB, r.n)
}

func (s *meterStats) add(db float64, speaking bool) {
	if math.IsInf(db, -1) {
		return
	}
	if speaking {
		s.speech.add(db)
	} else {
		s.quiet.add(db)
	}
}

func (s *meterStats) log() {
	if s.speech.n == 0 && s.quiet.n == 0 {
		return
	}
	msg := fmt.Sprintf("mic_level_dbfs: speech[%s] quiet[%s] window=%.0f..%.0f band=%.0f..%.0f",
		&s.speech, &s.quiet, meterFloorDB, meterCeilDB, meterHighPassHz, meterLowPassHz)
	if s.speech.n > 0 && s.quiet.n > 0 {
		msg += fmt.Sprintf(" snr=%.1f", s.speech.avg()-s.quiet.avg())
	}
	log.Info(msg)
}

func (r *recordingSession) Start() error {
	r.capture.SetCallback(r.onAudio)
	if err := r.capture.Start(); err != nil {
		r.capture.ClearCallback()
		return err
	}
	go r.monitorSilence()
	go r.awaitStop()
	return nil
}

func (r *recordingSession) monitorSilence() {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			switch r.mon.Tick(r.vp.HasSpeechTick()) {
			case SilenceWarn:
				log.Info("no_voice_warning")
				tray.SetWarning(true)
				overlay.SetState(overlay.Silent)
				audio.PlayError()
			case SilenceWarnClear:
				tray.SetWarning(false)
				overlay.SetState(overlay.Recording)
			case SilenceRepeat:
				log.Info("silence_during_warning")
				audio.PlayError()
			case SilenceAutoClose:
				r.markReleased()
				log.Info("silence_auto_close")
				audio.PlayEnd()
				tray.SetRecording(false)
				r.autoClosed.Store(true)
				r.close()
				return
			}
		}
	}
}

func (r *recordingSession) awaitStop() {
	select {
	case <-r.stop:
	case <-r.done:
		return
	}
	r.markReleased()
	log.Info("recording_stop")
	audio.PlayEnd() // reflexive: sound the release before the tray/icon update (playOne is non-blocking)
	tray.SetRecording(false)
	// Keep the mic open a beat after release so a fast keyup doesn't clip the
	// last word: onAudio keeps feeding until close() below stops capture.
	if r.tailWait > 0 {
		time.Sleep(r.tailWait)
	}
	r.close()
}

func (r *recordingSession) close() {
	r.closeOnce.Do(func() { close(r.done) })
}

// markReleased stamps the moment recording ended, first writer wins: both stop
// paths can run, and the earlier one is the instant the user stopped talking.
func (r *recordingSession) markReleased() {
	r.releasedAt.CompareAndSwap(0, time.Now().UnixNano())
}

// ReleasedAt reports when recording ended, or the zero time if it never did.
func (r *recordingSession) ReleasedAt() time.Time {
	ns := r.releasedAt.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

func (r *recordingSession) Wait() {
	<-r.done

	r.capture.Stop()
	r.capture.ClearCallback()

	r.mu.Lock()
	r.stopped = true
	r.stats.log()
	r.mu.Unlock()
}
