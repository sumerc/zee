package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	cacheFile     = "update_check.json"
	cacheTTL      = 24 * time.Hour
	checkInterval = 5 * time.Minute
)

type ghRelease struct {
	TagName string `json:"tag_name"`
}

type cachedCheck struct {
	Version   string `json:"version"`
	CheckedAt int64  `json:"checked_at"`
}

func CheckLatest(currentVersion string) (*Release, error) {
	if currentVersion == "dev" {
		return nil, nil
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", Repo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("github api: %s", resp.Status)
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}

	r := &Release{Version: rel.TagName, URL: ReleaseURL(rel.TagName)}
	if !r.NewerThan(currentVersion) {
		return nil, nil
	}
	return r, nil
}

func cachePath(cacheDir string) string {
	return filepath.Join(cacheDir, cacheFile)
}

func readCache(cacheDir string) (*Release, bool) {
	data, err := os.ReadFile(cachePath(cacheDir))
	if err != nil {
		return nil, false
	}
	var c cachedCheck
	if json.Unmarshal(data, &c) != nil {
		return nil, false
	}
	if time.Since(time.Unix(c.CheckedAt, 0)) > cacheTTL {
		return nil, false
	}
	if c.Version == "" {
		return nil, true
	}
	return &Release{Version: c.Version, URL: ReleaseURL(c.Version)}, true
}

func writeCache(cacheDir string, rel *Release) {
	c := cachedCheck{CheckedAt: time.Now().Unix()}
	if rel != nil {
		c.Version = rel.Version
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	_ = os.MkdirAll(cacheDir, 0755)
	_ = os.WriteFile(cachePath(cacheDir), data, 0644)
}

func CheckLatestCached(currentVersion, cacheDir string) (*Release, error) {
	if currentVersion == "dev" {
		return nil, nil
	}
	if rel, ok := readCache(cacheDir); ok {
		return rel, nil
	}
	rel, err := CheckLatest(currentVersion)
	if err != nil {
		return nil, err
	}
	writeCache(cacheDir, rel)
	return rel, nil
}

// CheckNow forces a fresh check, bypassing cache. Writes result to cache.
func CheckNow(currentVersion, cacheDir string) (*Release, error) {
	rel, err := CheckLatest(currentVersion)
	if err != nil {
		return nil, err
	}
	writeCache(cacheDir, rel)
	return rel, nil
}

func StartBackgroundCheck(currentVersion, cacheDir string, notify func(Release)) {
	if currentVersion == "dev" {
		return
	}
	go func() {
		check := func() {
			rel, err := CheckLatestCached(currentVersion, cacheDir)
			if err == nil && rel != nil {
				notify(*rel)
			}
		}
		check()
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()
		for range ticker.C {
			check()
		}
	}()
}
