# Changelog

## Unreleased

- felt_latency log line now itemizes the release→text window: tail wait, mic stop, PCM convert, inference, clipboard-save fork (duration + actual wait), paste copy/keystroke, and an unaccounted remainder
- Feedback beeps no longer block the caller on macOS: AVAudioPlayer's `-play` stalls while it starts the audio hardware, which sat on the push-to-talk path at both press and release; now dispatched to a serial queue, so the tone is unchanged but the wait is gone
- macOS clipboard is now native NSPasteboard + CGEvent instead of pbcopy/pbpaste forks and keybd_event: cuts ~255 ms of serial paste latency per dictation (copy 141 ms → 0.2 ms, keystroke 114 ms → ~0 ms, the latter a hardcoded sleep in keybd_event); drops the `micmonay/keybd_event` dependency
## v0.4.0

- Recording overlay wnd
- Local Whisper/Parakeet models running on GGML (near ~subsecond on M1)
- Better installation UX
- Lots of minor improvements/fixes
