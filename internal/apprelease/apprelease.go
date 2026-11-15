// Package apprelease orders the release strings an app reports.
package apprelease

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

type version struct {
	core [3]uint64
	pre  []string
}

// parse reads a semantic version with an optional leading "v", up to three
// numeric core parts and an optional pre-release. Build metadata is ignored.
func parse(s string) (version, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var v version
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil || p == "" {
			return v, false
		}
		v.core[i] = n
	}
	if hasPre {
		if pre == "" {
			return v, false
		}
		v.pre = strings.Split(pre, ".")
		for _, id := range v.pre {
			if id == "" {
				return v, false
			}
		}
	}
	return v, true
}

func compareSemver(a, b version) int {
	for i := range a.core {
		if a.core[i] != b.core[i] {
			if a.core[i] > b.core[i] {
				return 1
			}
			return -1
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1 // a release sorts above its own pre-releases
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		if c := comparePreID(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(a.pre), len(b.pre))
}

func comparePreID(a, b string) int {
	an, aerr := strconv.ParseUint(a, 10, 64)
	bn, berr := strconv.ParseUint(b, 10, 64)
	switch {
	case aerr == nil && berr == nil:
		if an != bn {
			if an > bn {
				return 1
			}
			return -1
		}
		return 0
	case aerr == nil:
		return -1 // numeric identifiers sort below alphanumeric ones
	case berr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func cmpInt(a, b int) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	}
	return 0
}

// CompareReleases returns 1, 0 or -1 as a sorts after, with or before b.
// Releases that both parse as semantic versions compare as versions. Any other
// pair compares by the time each release was first seen (an unknown release
// counts as never seen, so it sorts oldest); ties fall back to string order so
// the result is deterministic.
func CompareReleases(a, b string, firstSeen map[string]time.Time) int {
	if va, ok := parse(a); ok {
		if vb, ok := parse(b); ok {
			return compareSemver(va, vb)
		}
	}
	return compareSeen(a, b, firstSeen)
}

func compareSeen(a, b string, firstSeen map[string]time.Time) int {
	ta, tb := firstSeen[a], firstSeen[b]
	switch {
	case ta.After(tb):
		return 1
	case ta.Before(tb):
		return -1
	}
	return strings.Compare(a, b)
}

func allSemver(releases []string) bool {
	for _, r := range releases {
		if _, ok := parse(r); !ok {
			return false
		}
	}
	return true
}

// SortNewestFirst orders a release set newest first. One mode applies to the
// whole set (semver when every release parses, first-seen otherwise), so the
// order stays transitive.
func SortNewestFirst(releases []string, firstSeen map[string]time.Time) []string {
	out := append([]string(nil), releases...)
	semver := allSemver(out)
	sort.SliceStable(out, func(i, j int) bool {
		if semver {
			va, _ := parse(out[i])
			vb, _ := parse(out[j])
			if c := compareSemver(va, vb); c != 0 {
				return c > 0
			}
			return strings.Compare(out[i], out[j]) > 0
		}
		return compareSeen(out[i], out[j], firstSeen) > 0
	})
	return out
}

// Newest returns the newest release of the set, or "" when it is empty.
func Newest(releases []string, firstSeen map[string]time.Time) string {
	if len(releases) == 0 {
		return ""
	}
	return SortNewestFirst(releases, firstSeen)[0]
}
