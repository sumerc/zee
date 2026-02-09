package update

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	Repo       = "sumerc/zee"
	BinaryName = "zee"
)

type Release struct {
	Version     string
	AssetURL    string
	ChecksumURL string
}

type semver struct {
	major, minor, patch int
}

func parseSemver(v string) (semver, error) {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
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
	return semver{major, minor, patch}, nil
}

func (s semver) greaterThan(o semver) bool {
	if s.major != o.major {
		return s.major > o.major
	}
	if s.minor != o.minor {
		return s.minor > o.minor
	}
	return s.patch > o.patch
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
