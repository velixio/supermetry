package ingest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
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
		{name: "one exact host", hosts: []string{"supermetry.com"}, want: false},
		{name: "two exact hosts", hosts: []string{"supermetry.com", "docs.supermetry.com"}, want: true},
		{name: "one wildcard host", hosts: []string{"*.supermetry.com"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := multiHost(sites.Site{AllowedHosts: tt.hosts}); got != tt.want {
				t.Errorf("multiHost() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestDimensionIDCapsEveryAttackerControlledKind(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	const dailyLimit = 3
	h := &Handler{
		Dict:                      dict.New(pool),
		DimensionCardinalityLimit: dailyLimit,
	}
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	kinds := []dict.Kind{
		dict.Path, dict.Referrer, dict.Source, dict.Medium, dict.Campaign,
		dict.UTMContent, dict.UTMTerm, dict.EventName, dict.PropKey,
		dict.Region, dict.City, dict.BrowserVersion, dict.OSVersion,
		dict.Language, dict.Hostname,
	}
	before := DimensionOverflow.Load()
	for _, kind := range kinds {
		var overflowID int32
		for i := 0; i <= dailyLimit; i++ {
			value := fmt.Sprintf("%d-%d-%s", kind, i, strings.Repeat("x", 300))
			id, err := h.dimensionID(ctx, 42, day, kind, value)
			if err != nil {
				t.Fatalf("kind %d value %d: %v", kind, i, err)
			}
			if i < dailyLimit {
				if id == 0 || id == overflowID {
					t.Fatalf("kind %d legitimate value %d folded early to %d", kind, i, id)
				}
				continue
			}
			overflowID = id
			if got, ok := h.Dict.Value(kind, id); !ok || got != OtherValue {
				t.Fatalf("kind %d overflow = %q, %v, want %q", kind, got, ok, OtherValue)
			}
		}
	}
	if delta := DimensionOverflow.Load() - before; delta != uint64(len(kinds)) {
		t.Fatalf("overflow counter delta = %d, want %d", delta, len(kinds))
	}
}

func TestDimensionIDCountsDictionaryOverflow(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	h := &Handler{
		Dict:                      dict.New(pool, dict.WithMaxEntriesPerKind(2)),
		DimensionCardinalityLimit: 10,
	}
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	before := DimensionOverflow.Load()
	if _, err := h.dimensionID(ctx, 42, day, dict.Path, "/first"); err != nil {
		t.Fatal(err)
	}
	id, err := h.dimensionID(ctx, 42, day, dict.Path, "/dictionary-overflow")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := h.Dict.Value(dict.Path, id); !ok || got != OtherValue {
		t.Fatalf("dictionary overflow = %q, %v, want %q", got, ok, OtherValue)
	}
	if delta := DimensionOverflow.Load() - before; delta != 1 {
		t.Fatalf("overflow counter delta = %d, want 1", delta)
	}
}

func TestDimensionIDCountsPerSiteShareOverflow(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	const limit = 4002
	h := &Handler{
		Dict:                      dict.New(pool, dict.WithMaxEntriesPerKind(limit)),
		DimensionCardinalityLimit: limit,
	}
	values := make([]string, limit/2+1)
	for i := range values {
		values[i] = fmt.Sprintf("/share/%d", i)
	}
	if _, err := h.Dict.IDsForSite(ctx, 42, dict.Path, values); err != nil {
		t.Fatal(err)
	}

	before := DimensionOverflow.Load()
	id, err := h.dimensionID(ctx, 42, time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC), dict.Path, "/over-share")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := h.Dict.Value(dict.Path, id); !ok || got != OtherValue {
		t.Fatalf("share overflow = %q, %v, want %q", got, ok, OtherValue)
	}
	if delta := DimensionOverflow.Load() - before; delta != 1 {
		t.Fatalf("overflow counter delta = %d, want 1", delta)
	}
}
