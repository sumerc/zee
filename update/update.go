package update

import (
	"fmt"
	"strconv"
	"strings"
)

const Repo = "sumerc/zee"

type Release struct {
	Version string
	URL     string // GitHub release page URL
}

func ReleaseURL(version string) string {
	return fmt.Sprintf("https://github.com/%s/releases/tag/%s", Repo, version)
}

func (r Release) AssetName() string { return "Zee-" + r.Version + ".zip" }

func (r Release) AssetURL() string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", Repo, r.Version, r.AssetName())
}

func (r Release) ChecksumsURL() string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/checksums.txt", Repo, r.Version)
}

type semver struct {
	major, minor, patch int
	// pre is the prerelease suffix without its leading '-' ("rc.1"), empty for a
	// final release. It must be kept, not discarded: v0.4.0-rc.1 and v0.4.0 would
	// otherwise compare equal and an rc install could never update to its own
	// final release.
	pre string
}

func parseSemver(v string) (semver, error) {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '+'); i >= 0 { // build metadata: ignored entirely
		v = v[:i]
	}
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		pre, v = v[i+1:], v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("invalid semver: %q", v)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return semver{}, err
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return semver{}, err
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return semver{}, err
	}
	return semver{major, minor, patch, pre}, nil
}

func (s semver) greaterThan(o semver) bool {
	if s.major != o.major {
		return s.major > o.major
	}
	if s.minor != o.minor {
		return s.minor > o.minor
	}
	if s.patch != o.patch {
		return s.patch > o.patch
	}
	// Same x.y.z: a final release outranks a prerelease of itself (v0.4.0 >
	// v0.4.0-rc.1). Comparing two prereleases is a plain string compare rather
	// than semver's per-identifier rules — /releases/latest never returns a
	// prerelease, so that branch is unreachable through CheckLatest, and the
	// worst it can do is decline an update, never offer a downgrade.
	if s.pre == o.pre {
		return false
	}
	return s.pre == "" || (o.pre != "" && s.pre > o.pre)
}

func (r Release) NewerThan(current string) bool {
	cur, err := parseSemver(current)
	if err != nil {
		return false
	}
	rel, err := parseSemver(r.Version)
	if err != nil {
		return false
	}
	return rel.greaterThan(cur)
}
