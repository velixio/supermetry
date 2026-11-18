package approllup_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"supermetry/internal/appfixture"
	"supermetry/internal/approllup"
)

func tagRows(t *testing.T, f *appfixture.Fixture, issue string) []string {
	t.Helper()
	rows, err := f.Pool.Query(context.Background(), `select key || '=' || value || ':' || events || ' on ' || to_char(last_seen at time zone 'UTC', 'MM-DD HH24:MI')
from app_issue_tags where issue_id = $1 order by key, value`, f.Issues[issue])
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func TestIssueTagRollupHandComputedAndIdempotent(t *testing.T) {
	f := appfixture.New(t)
	recompute := func() {
		for _, day := range []string{"2026-11-12", "2026-11-13", "2026-11-14", "2026-11-15"} {
			d, _ := time.Parse(time.DateOnly, day)
			if err := approllup.RecomputeDay(context.Background(), f.Pool, f.SiteID, d); err != nil {
				t.Fatal(err)
			}
		}
	}
	recompute()
	// I2: e5 (1.9.0, production, linux 24.04 cpu), e10 x3 (1.10.0, beta, linux
	// 24.04 cpu), e12 (1.10.0, production, macos 14.5 cpu). A runtime without
	// the web platform is not a browser.
	want := []string{
		"device_class=cpu:5 on 11-15 08:45",
		"environment=beta:3 on 11-15 08:30",
		"environment=production:2 on 11-15 08:45",
		"level=error:5 on 11-15 08:45",
		"os=14.5:1 on 11-15 08:45",
		"os=24.04:4 on 11-15 08:30",
		"platform=linux:4 on 11-15 08:30",
		"platform=macos:1 on 11-15 08:45",
		"release=1.10.0:4 on 11-15 08:45",
		"release=1.9.0:1 on 11-14 03:05",
	}
	if got := tagRows(t, f, "I2"); !reflect.DeepEqual(got, want) {
		t.Fatalf("I2 tags =\n%v\nwant\n%v", got, want)
	}
	recompute() // idempotent: same rows, no doubling
	if got := tagRows(t, f, "I2"); !reflect.DeepEqual(got, want) {
		t.Fatalf("after a second recompute I2 tags =\n%v", got)
	}
	// A late event on an old day updates the issue's totals on the next recompute.
	f.Insert(t, appfixture.Event{ID: "late", Kind: "error", Release: "1.9.0", Platform: "linux", At: time.Date(2026, 11, 13, 2, 0, 0, 0, time.UTC), Install: "A", Issue: "I2", Occ: 2})
	d, _ := time.Parse(time.DateOnly, "2026-11-13")
	if err := approllup.RecomputeDay(context.Background(), f.Pool, f.SiteID, d); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range tagRows(t, f, "I2") {
		if r == "release=1.9.0:3 on 11-14 03:05" {
			found = true
		}
	}
	if !found {
		t.Errorf("late event not counted: %v", tagRows(t, f, "I2"))
	}
}
