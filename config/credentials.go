package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"zee/log"
)

// credentials.json holds per-provider API keys, e.g. {"groq":"gsk_…"}. It lives
// beside config.json in Dir(), is written 0600, and is the ONLY source of keys —
// there is no environment-variable fallback. It is never logged or printed;
// callers report presence, never the value.
const credentialsFile = "credentials.json"

var credMu sync.Mutex

// credErr records why the last read of credentials.json failed (nil when the
// file is valid or simply absent). A malformed file yields no keys at all,
// which looks exactly like "never configured" — every provider drops out of
// Available() and the API is called with an empty key. Callers use this to tell
// the user which of the two it is instead of relaying the provider's misleading
// "invalid api key". Guarded by credMu.
var credErr error

func credentialsPath() string { return filepath.Join(Dir(), credentialsFile) }

// CredentialsPath is the on-disk credentials.json path (for the tray
// "Edit Credentials…" item to open in an editor).
func CredentialsPath() string { return credentialsPath() }

// readCredentials returns the stored provider→key map (empty on missing/corrupt
// file) and refreshes credErr. Caller holds credMu.
func readCredentials() map[string]string {
	m := map[string]string{}
	credErr = nil
	data, err := os.ReadFile(credentialsPath())
	if err != nil {
		if !os.IsNotExist(err) {
			credErr = err
			log.Warnf("credentials: read: %v", err)
		}
		return m
	}
	if err := json.Unmarshal(data, &m); err != nil {
		credErr = fmt.Errorf("%s is not valid JSON: %w", credentialsFile, err)
		log.Warnf("credentials: corrupt %s, ignoring: %v", credentialsFile, err)
		return map[string]string{}
	}
	return m
}

// CredentialsError reports why credentials.json could not be read, or nil when
// it is valid or absent. It re-reads the file, so the answer reflects the file
// as it is now — a hand edit is picked up without a restart.
func CredentialsError() error {
	credMu.Lock()
	defer credMu.Unlock()
	readCredentials()
	return credErr
}

// EnsureCredentials creates an empty credentials.json (0600) when none exists,
// so "Edit Credentials…" always has a file to open. An existing file is left
// untouched — including a malformed one, which is precisely what the user
// opened the editor to fix.
func EnsureCredentials() error {
	credMu.Lock()
	defer credMu.Unlock()
	if _, err := os.Stat(credentialsPath()); err == nil {
		return nil
	}
	return writeCredentials(map[string]string{})
}

// APIKey returns the stored API key for a provider (e.g. "groq"), or "" if none.
func APIKey(provider string) string {
	credMu.Lock()
	defer credMu.Unlock()
	return readCredentials()[provider]
}

// HasAPIKey reports whether a provider has a stored key, without exposing it.
func HasAPIKey(provider string) bool { return APIKey(provider) != "" }

// SetAPIKey stores a provider's API key (or removes it when key==""), writing
// credentials.json atomically at 0600. Read-modify-write, so other providers'
// keys are preserved.
func SetAPIKey(provider, key string) error {
	credMu.Lock()
	defer credMu.Unlock()

	m := readCredentials()
	if key == "" {
		delete(m, provider)
	} else {
		m[provider] = key
	}
	return writeCredentials(m)
}

func writeCredentials(m map[string]string) error {
	d := Dir()
	if err := os.MkdirAll(d, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(d, ".credentials-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op after a successful rename

	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, credentialsPath())
}
