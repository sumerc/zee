package update

import "testing"

func TestParseSemver(t *testing.T) {
	tests := []struct {
		input   string
		want    semver
		wantErr bool
	}{
		{"1.2.3", semver{1, 2, 3, ""}, false},
		{"v0.1.5", semver{0, 1, 5, ""}, false},
		{"v1.0.0-dirty", semver{1, 0, 0, "dirty"}, false},
		{"v2.3.4-rc1+build", semver{2, 3, 4, "rc1"}, false},
		{"v0.4.0-rc.1", semver{0, 4, 0, "rc.1"}, false},
		{"dev", semver{}, true},
		{"", semver{}, true},
		{"1.2", semver{}, true},
	}

	for _, tt := range tests {
		got, err := parseSemver(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseSemver(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("parseSemver(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestReleaseNewerThan(t *testing.T) {
	tests := []struct {
		release string
		current string
		want    bool
	}{
		{"v0.2.0", "v0.1.5", true},
		{"v0.1.5", "v0.1.5", false},
		{"v0.1.4", "v0.1.5", false},
		{"v1.0.0", "v0.9.9", true},
		{"v0.1.6", "v0.1.5-dirty", true},
		{"v0.1.5", "dev", false},
		{"invalid", "v0.1.5", false},
		// A prerelease install must be offered its own final release, and never
		// the reverse — the whole point of tagging an rc for a clean-machine test.
		{"v0.4.0", "v0.4.0-rc.1", true},
		{"v0.4.0-rc.1", "v0.4.0", false},
		{"v0.4.0-rc.1", "v0.4.0-rc.1", false},
	}

	for _, tt := range tests {
		r := Release{Version: tt.release}
		got := r.NewerThan(tt.current)
		if got != tt.want {
			t.Errorf("Release{%q}.NewerThan(%q) = %v, want %v", tt.release, tt.current, got, tt.want)
		}
	}
}

func TestReleaseURLs(t *testing.T) {
	r := Release{Version: "v1.2.3"}
	if got, want := r.AssetName(), "Zee-v1.2.3.zip"; got != want {
		t.Fatalf("AssetName() = %q, want %q", got, want)
	}
	if got, want := r.AssetURL(), "https://github.com/sumerc/zee/releases/download/v1.2.3/Zee-v1.2.3.zip"; got != want {
		t.Fatalf("AssetURL() = %q, want %q", got, want)
	}
	if got, want := r.ChecksumsURL(), "https://github.com/sumerc/zee/releases/download/v1.2.3/checksums.txt"; got != want {
		t.Fatalf("ChecksumsURL() = %q, want %q", got, want)
	}
}

// A working-copy build is ahead of its base tag, so no update is offered for it.
// Without this, the semver prerelease ordering would tell a developer running
// v0.3.8-89-g6537b3f that v0.3.8 is an upgrade.
func TestCheckLatestSkipsWorkingCopyBuilds(t *testing.T) {
	for _, v := range []string{
		"dev",
		"v0.3.8-89-g6537b3f",
		"v0.3.8-89-g6537b3f-dirty",
		"v0.3.8-1-gabc1234",
	} {
		rel, err := CheckLatest(v) // must not reach the network
		if err != nil || rel != nil {
			t.Errorf("CheckLatest(%q) = %v, %v; want nil, nil", v, rel, err)
		}
	}
	// A real prerelease tag is NOT a working-copy build and must still check.
	if !describeSuffix.MatchString("v0.3.8-89-g6537b3f") || describeSuffix.MatchString("v0.4.0-rc.1") {
		t.Error("describeSuffix must match git-describe output but not an rc tag")
	}
}
