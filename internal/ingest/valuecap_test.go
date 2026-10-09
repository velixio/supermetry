package ingest

import (
	"fmt"
	"testing"
	"time"

	"supermetry/internal/dict"
	"supermetry/internal/sites"
)

func TestValueCapFoldsAfterLimitPerSiteAndDay(t *testing.T) {
	c := newValueCap(200)
	day1 := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	day2 := day1.AddDate(0, 0, 1)
	for i := 1; i <= 200; i++ {
		value := fmt.Sprintf("Chrome %d", i)
		if got := c.Admit(1, day1, dict.BrowserVersion, value); got != value {
			t.Fatalf("value %d folded early: %q", i, got)
		}
	}
	tests := []struct {
		name  string
		site  int32
		day   time.Time
		kind  dict.Kind
		value string
		want  string
	}{
		{name: "201st folds", site: 1, day: day1, kind: dict.BrowserVersion, value: "Chrome 201", want: OtherValue},
		{name: "admitted value stays", site: 1, day: day1, kind: dict.BrowserVersion, value: "Chrome 7", want: "Chrome 7"},
		{name: "kinds independent", site: 1, day: day1, kind: dict.Language, value: "en", want: "en"},
		{name: "sites independent", site: 2, day: day1, kind: dict.BrowserVersion, value: "Chrome 201", want: "Chrome 201"},
		{name: "next day resets", site: 1, day: day2, kind: dict.BrowserVersion, value: "Chrome 201", want: "Chrome 201"},
		{name: "empty stays empty", site: 1, day: day2, kind: dict.BrowserVersion, value: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.Admit(tt.site, tt.day, tt.kind, tt.value); got != tt.want {
				t.Errorf("Admit() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValueCapRetainsOnlyCurrentAndPreviousDays(t *testing.T) {
	c := newValueCap(200)
	day1 := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	day2 := day1.AddDate(0, 0, 1)
	day3 := day2.AddDate(0, 0, 1)

	c.Admit(1, day1, dict.Language, "en")
	c.Admit(1, day2, dict.Language, "fr")
	c.Admit(1, day3, dict.Language, "de")
	c.Admit(2, day1, dict.Language, "hi")

	for key := range c.seen {
		if key.day < c.newest-1 {
			t.Fatalf("stale cap day %d retained with newest %d", key.day, c.newest)
		}
	}
	if len(c.seen) != 2 {
		t.Fatalf("retained %d cap sets, want current and previous days only", len(c.seen))
	}
}

func TestMultiHostIncludesWildcardSites(t *testing.T) {
	tests := []struct {
		name  string
		hosts []string
		want  bool
	}{
		{name: "one exact host", hosts: []string{"example.com"}, want: false},
		{name: "two exact hosts", hosts: []string{"example.com", "docs.example.com"}, want: true},
		{name: "one wildcard host", hosts: []string{"*.example.com"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := multiHost(sites.Site{AllowedHosts: tt.hosts}); got != tt.want {
				t.Errorf("multiHost() = %t, want %t", got, tt.want)
			}
		})
	}
}
