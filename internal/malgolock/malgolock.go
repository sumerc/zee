// Package malgolock serializes miniaudio (malgo) device-lifecycle calls across
// the whole process.
//
// miniaudio's CoreAudio backend keeps process-global state for default-device
// tracking (so it can follow Bluetooth connect/disconnect and sleep/wake), and
// ma_device_init/uninit/start/stop mutate it. They are NOT safe to call
// concurrently from two threads. The audio (capture) and beep (playback)
// packages each own a separate malgo.Context but share that global state, so
// without a process-wide lock a capture re-init can race a beep device stop and
// corrupt the heap (observed SIGSEGV in ma_device_uninit).
//
// Take this lock around any malgo context/device lifecycle call. It does NOT
// guard the audio data callback (that runs on miniaudio's own thread and does
// not touch lifecycle state). The lock is non-reentrant: acquire it once per
// logical operation, never nest.
package malgolock

import "sync"

var mu sync.Mutex

func Lock()   { mu.Lock() }
func Unlock() { mu.Unlock() }
