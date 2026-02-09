package update

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func Apply(rel *Release) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}
	dir := filepath.Dir(execPath)

	// Download to temp file in same directory (same filesystem for atomic rename)
	tmpFile, err := os.CreateTemp(dir, ".zee-update-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath) // cleanup on any error path

	resp, err := http.Get(rel.AssetURL)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("download binary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		tmpFile.Close()
		return fmt.Errorf("download binary: %s", resp.Status)
	}

	hasher := sha256.New()
	src := io.Reader(resp.Body)
	if resp.ContentLength > 0 {
		src = &progressReader{r: resp.Body, total: resp.ContentLength}
	}
	if _, err := io.Copy(io.MultiWriter(tmpFile, hasher), src); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write binary: %w", err)
	}
	if resp.ContentLength > 0 {
		fmt.Println() // newline after progress
	}
	tmpFile.Close()
	actualHash := hex.EncodeToString(hasher.Sum(nil))

	// Verify checksum
	if rel.ChecksumURL != "" {
		expectedHash, err := fetchExpectedHash(rel.ChecksumURL, assetName())
		if err != nil {
			return fmt.Errorf("fetch checksums: %w", err)
		}
		if actualHash != expectedHash {
			return fmt.Errorf("checksum mismatch: got %s, want %s", actualHash[:12], expectedHash[:12])
		}
	}

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	// Atomic swap: current -> .old, new -> current, remove .old
	oldPath := execPath + ".old"
	if err := os.Rename(execPath, oldPath); err != nil {
		return fmt.Errorf("backup current binary: %w", err)
	}
	if err := os.Rename(tmpPath, execPath); err != nil {
		// Rollback
		_ = os.Rename(oldPath, execPath)
		return fmt.Errorf("install new binary: %w", err)
	}
	_ = os.Remove(oldPath)
	return nil
}

type progressReader struct {
	r     io.Reader
	total int64
	read  int64
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	pct := float64(p.read) / float64(p.total) * 100
	fmt.Fprintf(os.Stderr, "\r  %.0f%% (%d / %d KB)", pct, p.read/1024, p.total/1024)
	return n, err
}

func fetchExpectedHash(checksumURL, filename string) (string, error) {
	resp, err := http.Get(checksumURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("checksums: %s", resp.Status)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		// Format: "<hash>  <filename>" or "<hash> <filename>"
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == filename {
			return parts[0], nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", filename)
}
