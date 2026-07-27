package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
)

type ghRelease struct {
	TagName string `json:"tag_name"`
}

// describeSuffix matches the tail `git describe` adds to a working-copy build:
// v0.3.8-89-g6537b3f, optionally -dirty. Such a build is AHEAD of its base tag,
// so semver's "prerelease sorts below release" rule would otherwise offer that
// base tag as an update to a developer running a newer binary than it.
var describeSuffix = regexp.MustCompile(`-\d+-g[0-9a-f]{7,40}(-dirty)?$`)

func CheckLatest(currentVersion string) (*Release, error) {
	if currentVersion == "dev" || describeSuffix.MatchString(currentVersion) {
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
