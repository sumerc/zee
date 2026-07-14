//go:build darwin

package update

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var updateHTTPClient = &http.Client{Timeout: 10 * time.Minute}

func Install(rel Release) error {
	if otherZeeRunning() {
		return fmt.Errorf("another Zee instance is running; quit it before updating")
	}
	app, err := currentAppPath()
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp(filepath.Dir(app), ".zee-update-")
	if err != nil {
		return fmt.Errorf("stage update beside %s: %w", app, err)
	}
	defer os.RemoveAll(work)

	archive := filepath.Join(work, rel.AssetName())
	if err := download(rel.AssetURL(), archive); err != nil {
		return err
	}
	want, err := releaseChecksum(rel.ChecksumsURL(), rel.AssetName())
	if err != nil {
		return err
	}
	if err := verifyChecksum(archive, want); err != nil {
		return err
	}

	unpacked := filepath.Join(work, "unpacked")
	if out, err := exec.Command("ditto", "-x", "-k", archive, unpacked).CombinedOutput(); err != nil {
		return fmt.Errorf("extract update: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	staged := filepath.Join(unpacked, "Zee.app")
	if err := validateBundle(staged, rel.Version); err != nil {
		return err
	}

	backup := filepath.Join(work, "previous.app")
	return swapBundles(app, staged, backup, func() error {
		cmd := exec.Command("open", "-n", app, "--args", "-wait-for-pid", strconv.Itoa(os.Getpid()))
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("launch updated Zee: %w (%s)", err, strings.TrimSpace(string(out)))
		}
		return nil
	})
}

func otherZeeRunning() bool {
	out, err := exec.Command("pgrep", "-x", "zee").Output()
	if err != nil {
		return false
	}
	self := os.Getpid()
	for _, field := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(field); err == nil && pid != self {
			return true
		}
	}
	return false
}

func currentAppPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	const marker = ".app/Contents/MacOS/"
	i := strings.Index(exe, marker)
	if i < 0 {
		return "", fmt.Errorf("updates require an installed Zee.app")
	}
	app := exe[:i+len(".app")]
	if filepath.Base(app) != "Zee.app" {
		return "", fmt.Errorf("unexpected app bundle %s", app)
	}
	return app, nil
}

func download(url, dest string) error {
	resp, err := updateHTTPClient.Get(url)
	if err != nil {
		return fmt.Errorf("download update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download update: %s", resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("download update: %w", err)
	}
	return f.Sync()
}

func releaseChecksum(url, name string) (string, error) {
	resp, err := updateHTTPClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("download checksums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download checksums: %s", resp.Status)
	}
	s := bufio.NewScanner(resp.Body)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) >= 2 && strings.TrimPrefix(fields[len(fields)-1], "*") == name {
			return fields[0], nil
		}
	}
	if err := s.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s is missing from checksums.txt", name)
}

func verifyChecksum(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("update checksum mismatch: got %s, want %s", got, want)
	}
	return nil
}

func validateBundle(app, version string) error {
	info := filepath.Join(app, "Contents", "Info.plist")
	identifier, err := plistValue(info, "CFBundleIdentifier")
	if err != nil || identifier != "com.zee.app" {
		return fmt.Errorf("invalid update bundle identifier %q", identifier)
	}
	gotVersion, err := plistValue(info, "CFBundleShortVersionString")
	if err != nil || gotVersion != strings.TrimPrefix(version, "v") {
		return fmt.Errorf("invalid update version %q, want %q", gotVersion, strings.TrimPrefix(version, "v"))
	}
	if out, err := exec.Command("codesign", "--verify", "--deep", "--strict", app).CombinedOutput(); err != nil {
		return fmt.Errorf("verify update signature: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func plistValue(path, key string) (string, error) {
	out, err := exec.Command("plutil", "-extract", key, "raw", "-o", "-", path).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read %s: %w (%s)", key, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func swapBundles(installed, staged, backup string, launch func() error) error {
	if err := os.Rename(installed, backup); err != nil {
		return fmt.Errorf("back up current app: %w", err)
	}
	if err := os.Rename(staged, installed); err != nil {
		_ = os.Rename(backup, installed)
		return fmt.Errorf("install updated app: %w", err)
	}
	if err := launch(); err != nil {
		failed := staged + ".failed"
		_ = os.Rename(installed, failed)
		if restoreErr := os.Rename(backup, installed); restoreErr != nil {
			return fmt.Errorf("%v; restore previous app: %w", err, restoreErr)
		}
		return err
	}
	return nil
}
