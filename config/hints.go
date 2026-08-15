package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const hintsFile = "hints.txt"

const hintsHeader = `# Vocabulary hints for transcription (one per line)
# These help the model recognize domain-specific terms
# Empty lines and lines starting with # are ignored
# A term may list spoken-form aliases after a colon, e.g. "CGo: seego, see go"
# — the post-transcription corrector maps those aliases to the term; only the
# term itself is sent to providers that take vocabulary hints
Opus
Claude
Sonnet
Fable
Pi.dev
JSON
Codex
Harness
Gzip
OpenAI
Anthropic
App Router
Grafana
favicon
Mistral
ElevenLabs
Bun
Node.js
`

func HintsPath() string {
	return filepath.Join(Dir(), hintsFile)
}

var (
	hintsCache   string
	hintsModTime time.Time
	hintsFixed   bool
)

func SetHints(s string) {
	hintsCache = s
	hintsFixed = true
}

func GetHints() string {
	if hintsFixed {
		return hintsCache
	}
	info, err := os.Stat(HintsPath())
	if err != nil {
		if os.IsNotExist(err) {
			os.MkdirAll(Dir(), 0755)
			os.WriteFile(HintsPath(), []byte(hintsHeader), 0644)
		}
		return hintsCache
	}
	if info.ModTime().Equal(hintsModTime) {
		return hintsCache
	}

	f, err := os.Open(HintsPath())
	if err != nil {
		return hintsCache
	}
	defer f.Close()

	var hints []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// "Term: alias1, alias2" — aliases are for the post-transcription
		// corrector only; providers get just the canonical term.
		if before, _, found := strings.Cut(line, ":"); found {
			line = strings.TrimSpace(before)
		}
		if line != "" {
			hints = append(hints, line)
		}
	}
	hintsCache = strings.Join(hints, ", ")
	hintsModTime = info.ModTime()
	return hintsCache
}

// HintLines returns the raw uncommented lines of hints.txt, alias syntax
// included, for the correction dictionary. Unlike GetHints it is unaffected
// by SetHints pinning: -no-hints keeps hints out of a model's prompt but must
// not disable post-transcription correction.
func HintLines() []string {
	f, err := os.Open(HintsPath())
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}
