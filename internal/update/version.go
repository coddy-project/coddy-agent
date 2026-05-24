package update

import (
	"regexp"
	"strings"
)

var semverPrefix = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)`)

// NormalizeSemver returns the leading X.Y.Z from a version string, or "" if none.
func NormalizeSemver(v string) string {
	v = strings.TrimSpace(v)
	m := semverPrefix.FindStringSubmatch(v)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// CompareSemver compares two version strings by their normalized X.Y.Z prefixes.
// Returns -1 if a < b, 0 if equal, 1 if a > b. Missing semver sorts before any valid semver.
func CompareSemver(a, b string) int {
	na, nb := NormalizeSemver(a), NormalizeSemver(b)
	if na == "" && nb == "" {
		return 0
	}
	if na == "" {
		return -1
	}
	if nb == "" {
		return 1
	}
	pa := strings.Split(na, ".")
	pb := strings.Split(nb, ".")
	for i := 0; i < 3; i++ {
		ai, bi := atoi(pa[i]), atoi(pb[i])
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return 0
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
