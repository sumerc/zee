# Design Notes

Ideas and tradeoffs decided while building zee. Each entry captures *why* a
choice was made, so we don't relitigate it (or silently regress it) later.

## Why the audio device is re-init'd on every recording

`audio.Start()` tears down the capture device (`Uninit`) and rebuilds it
(`InitDevice`) on **every** recording, instead of initializing it once at
startup and reusing it.

**Reason:** after macOS sleep/wake (and some device/route changes) the malgo
(miniaudio/CoreAudio) device handle goes **stale silently** — `device.Start()`
returns no error, but the mic produces only silence. There's no reliable signal
to detect this, so the blunt-but-safe fix is to rebuild the device every time.
This replaced an earlier "start, and only recreate on error" approach that
didn't work because the stale handle never reported an error.

**Tradeoff:** constant `Uninit`/`InitDevice` churn enlarges the surface for
miniaudio lifecycle bugs. It directly caused a double-free crash: if the rebuild
failed transiently, the old (already-freed) device pointer was retained and the
next `Start` uninited it a second time (SIGABRT/SIGSEGV in `ma_device_uninit`).
Fixed by never keeping a freed pointer — store `nil` if the rebuild fails (see
`audio/reinit.go`). The same teardown-on-use pattern (and the same fix) applies
to `beep` playback.

**Better long-term options (not yet done):**
- Init once + reinit only on a real signal — macOS `NSWorkspace` sleep/wake
  notification, or miniaudio's reroute/stop notification callback. Most correct,
  but only manually verifiable (`pmset sleepnow`, wake, record).
- Init once + stale-by-behavior detection — keep the device, and after `Start`
  watch the data callback for actual frames; reinit only if none arrive. Catches
  every stale cause, and is unit-testable with a fake device, but it's heuristic.

All miniaudio device lifecycle calls (capture + playback) are also serialized
behind a process-wide lock (`internal/malgolock`) as defense against concurrent
init/uninit across the two malgo contexts.
