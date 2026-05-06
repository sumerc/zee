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
		hints = append(hints, line)
	}
	hintsCache = strings.Join(hints, ", ")
	hintsModTime = info.ModTime()
	return hintsCache
}
