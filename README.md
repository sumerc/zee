# voca

Push-to-talk transcription using Groq Whisper API. Hold Ctrl+Shift+Space to record, release to transcribe to clipboard.

## Setup

```bash
make build
export GROQ_API_KEY=your_key
./voca
```

## Testing

```bash
make test                                      # unit tests
make integration-test WAV=testdata/short.wav   # requires GROQ_API_KEY
make benchmark WAV=file.wav RUNS=5             # multiple runs for timing

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-benchmark` | - | WAV file for benchmarking |
| `-runs` | 3 | Benchmark iterations |
| `-setup` | false | Select microphone device |
| `-autopaste` | true | Auto-paste after transcription |
