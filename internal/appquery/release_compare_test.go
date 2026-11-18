package appquery_test

import (
	"context"
	"testing"

	"supermetry/internal/appfixture"
	"supermetry/internal/appquery"
)

func TestCompareReleases(t *testing.T) {
	f := appfixture.New(t)
	seedDaily(t, f, "2026-11-14", "1.9.0", 1000, 20, 0, 0, 0)
	seedDaily(t, f, "2026-11-14", "1.10.0", 1000, 50, 0, 0, 0)
	q, scope := detailQuerier(f)
	fl := appquery.Filter{From: d("2026-11-14"), To: d("2026-11-14")}
	got, err := q.CompareReleases(context.Background(), scope, fl, "1.9.0", "1.10.0")
	if err != nil {
		t.Fatal(err)
	}
	near(t, "a", got.A.Health.CrashFreeSessions, 0.98)
	near(t, "b", got.B.Health.CrashFreeSessions, 0.95)
	near(t, "delta", got.Delta.CrashFreeSessions, -0.03)
	// I3 (e7) is seen only in 1.10.0; I2 is seen in both.
	if len(got.OnlyInB) != 1 || got.OnlyInB[0].ID != f.Issues["I3"] {
		t.Fatalf("only in b = %+v", got.OnlyInB)
	}
	if _, err := q.CompareReleases(context.Background(), scope, fl, "1.9.0", "7.7.7"); err == nil {
		t.Fatal("unknown release must fail")
	}
}

func TestFindRegressionRelease(t *testing.T) {
	f := appfixture.New(t)
	ctx := context.Background()
	for _, r := range []struct {
		rel               string
		sessions, crashed int
	}{{"2.0", 1000, 10}, {"2.1", 1000, 15}, {"2.2", 1000, 50}, {"2.3", 99, 50}} {
		seedDaily(t, f, "2026-11-14", r.rel, r.sessions, r.crashed, 0, 0, 0)
		// One ping so the release is listed.
		f.Insert(t, appfixture.Event{ID: "reg" + r.rel, Kind: "session", Release: r.rel, Platform: "linux", Session: "reg" + r.rel, Install: "A", At: f.Now.AddDate(0, 0, -1)})
	}
	q, scope := detailQuerier(f)
	fl := appquery.Filter{From: d("2026-11-14"), To: d("2026-11-14")}
	hit, err := q.FindRegressionRelease(ctx, scope, fl, 0)
	if err != nil {
		t.Fatal(err)
	}
	if hit == nil || hit.Release != "2.2" || hit.Previous != "2.1" {
		t.Fatalf("hit = %+v", hit)
	}
	near(t, "cfs", &hit.CrashFreeSessions, 0.95)
	near(t, "prev", &hit.PreviousCrashFreeSessions, 0.985)
	if hit.NewIssues == nil {
		t.Fatal("new issues must be a list")
	}
	// A bigger threshold ignores the 0.035 drop.
	if hit, err = q.FindRegressionRelease(ctx, scope, fl, 0.05); err != nil || hit != nil {
		t.Fatalf("hit = %+v err %v", hit, err)
	}
}
