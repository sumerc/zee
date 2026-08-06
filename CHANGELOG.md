# Changelog

## Unreleased

- felt_latency log line now itemizes the release→text window: tail wait, mic stop, PCM convert, inference, clipboard-save fork (duration + actual wait), paste copy/keystroke, and an unaccounted remainder
- macOS clipboard is now native NSPasteboard + CGEvent instead of pbcopy/pbpaste forks and keybd_event: cuts ~255 ms of serial paste latency per dictation (copy 141 ms → 0.2 ms, keystroke 114 ms → ~0 ms, the latter a hardcoded sleep in keybd_event)
## v0.4.0

- Recording overlay wnd
- Local Whisper/Parakeet models running on GGML (near ~subsecond on M1)
- Better installation UX
- Lots of minor improvements/fixes
