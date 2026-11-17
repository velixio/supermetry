package appquery_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"supermetry/internal/appfixture"
	"supermetry/internal/appquery"
	"supermetry/internal/approllup"
)

type productEnv struct {
	p     *appfixture.Product
	q     *appquery.Querier
	scope appquery.Scope
}

func productSetup(t *testing.T, rollTo string) productEnv {
	t.Helper()
	p := appfixture.NewProduct(t, "UTC")
	p.AddDataset(t, true)
	for _, day := range []string{"2026-10-06", "2026-10-07", "2026-10-08", "2026-10-09"} {
		if day > rollTo {
			break
		}
		if err := approllup.RecomputeDay(context.Background(), p.Pool, p.SiteID, d(day)); err != nil {
			t.Fatal(err)
		}
	}
	return productEnv{p: p, q: appquery.New(p.Pool, func() time.Time { return p.Now }),
		scope: appquery.Scope{SiteID: p.SiteID, TZ: p.Loc, RetentionDays: 90}}
}

func productRange() appquery.Filter {
	return appquery.Filter{From: d("2026-10-06"), To: d("2026-10-09")}
}

// 10-09 is the open day: its numbers come live from events, not from a rollup
// row, so rolling up to 10-08 only must give the same answers.
func TestActiveUsersHandComputed(t *testing.T) {
	e := productSetup(t, "2026-10-08")
	au, err := e.q.ActiveUsers(context.Background(), e.scope, productRange())
	if err != nil {
		t.Fatal(err)
	}
	var dau, wau, mau []int64
	for _, p := range au.Series {
		dau, wau, mau = append(dau, p.DAU), append(wau, p.WAU), append(mau, p.MAU)
	}
	if !reflect.DeepEqual(dau, []int64{3, 2, 2, 1}) {
		t.Fatalf("DAU = %v", dau)
	}
	if !reflect.DeepEqual(wau, []int64{3, 4, 4, 4}) || !reflect.DeepEqual(mau, []int64{3, 4, 4, 4}) {
		t.Fatalf("WAU = %v MAU = %v", wau, mau)
	}
	if au.Distinct != 4 {
		t.Fatalf("distinct = %d", au.Distinct)
	}
	near(t, "stickiness", au.Stickiness, 0.5)
	var fresh []int64
	for _, c := range au.NewInstalls {
		fresh = append(fresh, c.Count)
	}
	if !reflect.DeepEqual(fresh, []int64{3, 1, 0, 0}) {
		t.Fatalf("new installs = %v", fresh)
	}
}

func TestActiveUsersWindowsReachBeforeTheRange(t *testing.T) {
	e := productSetup(t, "2026-10-09")
	au, err := e.q.ActiveUsers(context.Background(), e.scope, appquery.Filter{From: d("2026-10-09"), To: d("2026-10-09")})
	if err != nil {
		t.Fatal(err)
	}
	if len(au.Series) != 1 || au.Series[0].DAU != 1 || au.Series[0].WAU != 4 || au.Series[0].MAU != 4 {
		t.Fatalf("series = %+v", au.Series)
	}
	if au.Distinct != 1 || au.NewInstalls[0].Count != 0 {
		t.Fatalf("distinct = %d new = %+v", au.Distinct, au.NewInstalls)
	}
}

func TestAdoptionHandComputed(t *testing.T) {
	e := productSetup(t, "2026-10-08")
	ctx := context.Background()
	rows, err := e.q.Adoption(ctx, e.scope, productRange(), "release")
	if err != nil {
		t.Fatal(err)
	}
	share := map[string]float64{}
	for _, r := range rows {
		share[r.Day.Format(time.DateOnly)+" "+r.Value] = r.Share
	}
	want := map[string]float64{
		"2026-10-06 1.0.0": 1, "2026-10-07 1.0.0": 1,
		"2026-10-08 1.0.0": 0.5, "2026-10-08 1.1.0": 0.5, "2026-10-09 1.1.0": 1,
	}
	if !reflect.DeepEqual(share, want) {
		t.Fatalf("release shares = %v, want %v", share, want)
	}

	for by, want := range map[string]map[string]int64{
		"platform": {"ios": 2, "android": 2},
		"country":  {"DE": 2, "US": 2},
		"os":       {"iOS": 2, "Android": 2},
		"device":   {"mobile": 4},
	} {
		totals, err := e.q.AdoptionTotals(ctx, e.scope, productRange(), by)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]int64{}
		for _, r := range totals {
			got[r.Value] = r.Installs
			if !r.Day.IsZero() {
				t.Fatalf("%s total has a day: %+v", by, r)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s totals = %v, want %v", by, got, want)
		}
	}
	platforms, _ := e.q.AdoptionTotals(ctx, e.scope, productRange(), "platform")
	for _, r := range platforms {
		near(t, r.Value+" share", &r.Share, 0.5)
	}
	if _, err := e.q.Adoption(ctx, e.scope, productRange(), "nope"); err == nil {
		t.Fatal("unknown dimension accepted")
	}
}

// Review Focus 2: one install on two platforms and two releases the same day is
// one DAU and counts once in each split; the totals over a range may sum to
// more than 1.
func TestAdoptionTwoPlatformsSameDay(t *testing.T) {
	p := appfixture.NewProduct(t, "UTC")
	at := func(h int) time.Time { return time.Date(2026, 10, 6, h, 0, 0, 0, time.UTC) }
	p.Add(t,
		appfixture.ProductHit{Install: "E", At: at(9), Screen: "/home", Platform: "ios", Release: "1.0.0"},
		appfixture.ProductHit{Install: "E", At: at(10), Screen: "/home", Platform: "macos", Release: "1.0.0"},
		appfixture.ProductHit{Install: "F", At: at(11), Screen: "/home", Platform: "ios", Release: "1.0.0"},
		appfixture.ProductHit{Install: "F", At: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC), Screen: "/home", Platform: "ios", Release: "1.1.0"},
	)
	for _, day := range []string{"2026-10-06", "2026-10-07"} {
		if err := approllup.RecomputeDay(context.Background(), p.Pool, p.SiteID, d(day)); err != nil {
			t.Fatal(err)
		}
	}
	q := appquery.New(p.Pool, func() time.Time { return p.Now })
	scope := appquery.Scope{SiteID: p.SiteID, TZ: p.Loc, RetentionDays: 90}
	f := appquery.Filter{From: d("2026-10-06"), To: d("2026-10-07")}
	au, err := q.ActiveUsers(context.Background(), scope, f)
	if err != nil {
		t.Fatal(err)
	}
	if au.Series[0].DAU != 2 || au.Distinct != 2 {
		t.Fatalf("DAU/distinct = %d/%d, want 2/2", au.Series[0].DAU, au.Distinct)
	}
	perDay, err := q.Adoption(context.Background(), scope, f, "platform")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, r := range perDay {
		got[r.Day.Format(time.DateOnly)+" "+r.Value] = r.Installs
	}
	// E's last platform that day wins: macos, once.
	if want := map[string]int64{"2026-10-06 ios": 1, "2026-10-06 macos": 1, "2026-10-07 ios": 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("platform per day = %v, want %v", got, want)
	}
	releases, err := q.AdoptionTotals(context.Background(), scope, f, "release")
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	for _, r := range releases {
		sum += r.Share
	}
	if sum <= 1 { // F is on 1.0.0 and on 1.1.0 within the range
		t.Fatalf("release shares sum to %v, want more than 1: %+v", sum, releases)
	}
}

func TestEngagementHandComputed(t *testing.T) {
	e := productSetup(t, "2026-10-08")
	ctx := context.Background()
	day6, err := e.q.Engagement(ctx, e.scope, appquery.Filter{From: d("2026-10-06"), To: d("2026-10-06")})
	if err != nil {
		t.Fatal(err)
	}
	if day6.Sessions != 3 {
		t.Fatalf("sessions = %d", day6.Sessions)
	}
	near(t, "mean session seconds", day6.MeanSessionSeconds, 60)
	near(t, "median session seconds", day6.MedianSessionSeconds, 60)
	near(t, "sessions per user", day6.SessionsPerUser, 1)
	near(t, "screens per session", day6.ScreensPerSession, 5.0/3)

	empty, err := e.q.Engagement(ctx, e.scope, appquery.Filter{From: d("2026-09-01"), To: d("2026-09-02")})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Sessions != 0 || empty.MeanSessionSeconds != nil || empty.SessionsPerUser != nil {
		t.Fatalf("empty = %+v", empty)
	}
	ios, err := e.q.Engagement(ctx, e.scope, appquery.Filter{From: d("2026-10-06"), To: d("2026-10-06"), Platform: "ios"})
	if err != nil {
		t.Fatal(err)
	}
	if ios.Sessions != 2 {
		t.Fatalf("ios sessions = %d", ios.Sessions)
	}
}

func TestActiveUsersFilterAndRange(t *testing.T) {
	e := productSetup(t, "2026-10-09")
	f := productRange()
	f.Platform = "android"
	au, err := e.q.ActiveUsers(context.Background(), e.scope, f)
	if err != nil {
		t.Fatal(err)
	}
	var dau []int64
	for _, p := range au.Series {
		dau = append(dau, p.DAU)
	}
	if !reflect.DeepEqual(dau, []int64{1, 1, 0, 0}) || au.Distinct != 2 {
		t.Fatalf("android DAU = %v distinct = %d", dau, au.Distinct)
	}
	if _, err := e.q.ActiveUsers(context.Background(), e.scope, appquery.Filter{}); err == nil {
		t.Fatal("empty range accepted")
	}
}
