package update

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type ghRelease struct {
	TagName string `json:"tag_name"`
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
