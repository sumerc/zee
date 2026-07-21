//go:build darwin

package update

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestSwapBundles(t *testing.T) {
	dir := t.TempDir()
	installed := filepath.Join(dir, "Zee.app")
	staged := filepath.Join(dir, "new.app")
	backup := filepath.Join(dir, "old.app")
	writeMarker(t, installed, "old")
	writeMarker(t, staged, "new")

	if err := swapBundles(installed, staged, backup); err != nil {
		t.Fatal(err)
	}
	assertMarker(t, installed, "new")
	assertMarker(t, backup, "old")
}

func TestSwapBundlesRollsBackInstallFailure(t *testing.T) {
	dir := t.TempDir()
	installed := filepath.Join(dir, "Zee.app")
	staged := filepath.Join(dir, "missing.app")
	backup := filepath.Join(dir, "old.app")
	writeMarker(t, installed, "old")

	if err := swapBundles(installed, staged, backup); err == nil {
		t.Fatal("expected install failure")
	}
	assertMarker(t, installed, "old")
}

func TestVerifyChecksum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.zip")
	data := []byte("zee update")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if err := verifyChecksum(path, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksum(path, "wrong"); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}

func writeMarker(t *testing.T, dir, value string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}

func assertMarker(t *testing.T, dir, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "marker"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("marker = %q, want %q", got, want)
	}
}
