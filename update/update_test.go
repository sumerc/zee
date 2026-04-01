package update

import "testing"

func TestParseSemver(t *testing.T) {
	tests := []struct {
		input   string
		want    semver
		wantErr bool
	}{
		{"1.2.3", semver{1, 2, 3}, false},
		{"v0.1.5", semver{0, 1, 5}, false},
		{"v1.0.0-dirty", semver{1, 0, 0}, false},
		{"v2.3.4-rc1+build", semver{2, 3, 4}, false},
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
	}

	for _, tt := range tests {
		r := Release{Version: tt.release}
		got := r.NewerThan(tt.current)
		if got != tt.want {
			t.Errorf("Release{%q}.NewerThan(%q) = %v, want %v", tt.release, tt.current, got, tt.want)
		}
	}
}
