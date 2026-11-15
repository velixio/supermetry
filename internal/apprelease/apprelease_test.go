package apprelease

import (
	"testing"
	"time"
)

func TestCompareReleases(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seen := map[string]time.Time{"nightly-7": t0.Add(48 * time.Hour), "alpha": t0, "1.0.0": t0.Add(24 * time.Hour)}
	cases := []struct {
		a, b string
		want int
	}{
		{"1.10.0", "1.9.0", 1},
		{"1.9.0", "1.10.0", -1},
		{"v2.0.0", "1.9.9", 1},
		{"1.0.0-beta", "1.0.0", -1},
		{"1.0.0-beta.2", "1.0.0-beta.10", -1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.2", "1.2.0", 0},
		{"1.2.3+build5", "1.2.3", 0},
		{"v1.2.3", "1.2.3", 0},
		{"nightly-7", "alpha", 1}, // not semver: first seen decides
		{"nightly-7", "1.0.0", 1}, // a mix falls back to first seen
		{"1.0.0", "nightly-7", -1},
		{"alpha", "alpha", 0},
		{"zzz", "yyy", 1}, // unknown to first-seen: string order is the last resort
	}
	for _, c := range cases {
		if got := CompareReleases(c.a, c.b, seen); got != c.want {
			t.Errorf("CompareReleases(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNewest(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := Newest([]string{"1.9.0", "1.10.0", "1.2.0"}, nil); got != "1.10.0" {
		t.Fatalf("semver newest = %q", got)
	}
	if got := Newest([]string{"a", "b"}, map[string]time.Time{"a": t0.Add(time.Hour), "b": t0}); got != "a" {
		t.Fatalf("first-seen newest = %q", got)
	}
	if got := Newest(nil, nil); got != "" {
		t.Fatalf("empty newest = %q", got)
	}
}

func TestSortNewestFirstUsesOneModeForTheWholeSet(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seen := map[string]time.Time{"1.0.0": t0.Add(3 * time.Hour), "2.0.0": t0, "dev": t0.Add(time.Hour)}
	got := SortNewestFirst([]string{"2.0.0", "dev", "1.0.0"}, seen)
	want := []string{"1.0.0", "dev", "2.0.0"} // dev is not semver, so first-seen orders all three
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	got = SortNewestFirst([]string{"1.9.0", "1.10.0", "v2.0.0"}, nil)
	want = []string{"v2.0.0", "1.10.0", "1.9.0"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("semver order = %v, want %v", got, want)
		}
	}
}
