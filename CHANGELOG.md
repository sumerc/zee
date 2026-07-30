# Changelog

## Unreleased

- Local Qwen3-ASR provider (POC, branch only): CPU/Accelerate, 30 languages
- Local providers no longer fail every recording when the model saved in
  config.json belongs to a different engine; they fall back to their own default

## v0.4.0

- Recording overlay wnd
- Local Whisper/Parakeet models running on GGML (near ~subsecond on M1)
- Better installation UX
- Lots of minor improvements/fixes
