package appproduct_test

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/appfixture"
	"supermetry/internal/appproduct"
	"supermetry/internal/appquery"
	"supermetry/internal/approllup"
	"supermetry/internal/closer"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/privacy"
	"supermetry/internal/query"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
	"supermetry/internal/writer"
)

// syntheticNow is after every local day of the dataset in both zones, so all
// days are closed and read from the rollups.
var syntheticNow = time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)

type syntheticClock struct{}

func (syntheticClock) Now() time.Time { return syntheticNow }

func syntheticDay(t time.Time) types.Date { return types.Date{Time: t} }

// TestSyntheticMatchesReference is the spec exit criterion "funnels and
// retention match on a synthetic app dataset": 500 installs over 30 days go
// through adapter, writer, CloseDay and RecomputeDay, and the product reads
// equal the independent in-Go reference exactly, in two time zones.
func TestSyntheticMatchesReference(t *testing.T) {
	hits := appfixture.SyntheticProduct(20261118, 500, 30)
	if len(hits) < 10000 {
		t.Fatalf("dataset too small: %d hits", len(hits))
	}
	for _, zone := range []string{"UTC", "Asia/Kolkata"} {
		t.Run(zone, func(t *testing.T) {
			loc, err := time.LoadLocation(zone)
			if err != nil {
				t.Fatal(err)
			}
			want := appfixture.ExpectedProduct(hits, loc)
			t.Logf("%d hits: distinct=%d funnel installs=%v sessions=%v cohorts=%d", len(hits), want.Distinct, want.FunnelInstalls, want.FunnelSessions, len(want.Cohorts))
			pool := syntheticIngest(t, zone, loc, hits)
			ctx := context.Background()

			for d := want.From; !d.After(want.To); d = d.AddDate(0, 0, 1) {
				if _, err := closer.CloseDay(ctx, pool, []int32{siteID}, d); err != nil {
					t.Fatal(err)
				}
				if err := approllup.RecomputeDay(ctx, pool, siteID, d); err != nil {
					t.Fatal(err)
				}
			}

			q := appquery.New(pool, func() time.Time { return syntheticNow })
			au, err := q.ActiveUsers(ctx, appquery.Scope{SiteID: siteID, TZ: loc}, appquery.Filter{From: want.From, To: want.To})
			if err != nil {
				t.Fatal(err)
			}
			var dau, wau, mau []int64
			for _, p := range au.Series {
				dau, wau, mau = append(dau, p.DAU), append(wau, p.WAU), append(mau, p.MAU)
			}
			if !reflect.DeepEqual(dau, want.DAU) {
				t.Errorf("DAU = %v\nwant  %v", dau, want.DAU)
			}
			if !reflect.DeepEqual(wau, want.WAU) {
				t.Errorf("WAU = %v\nwant  %v", wau, want.WAU)
			}
			if !reflect.DeepEqual(mau, want.MAU) {
				t.Errorf("MAU = %v\nwant  %v", mau, want.MAU)
			}
			if au.Distinct != want.Distinct {
				t.Errorf("distinct = %d, want %d", au.Distinct, want.Distinct)
			}
			if au.Stickiness == nil || math.Abs(*au.Stickiness-want.Stickiness) > 1e-9 {
				t.Errorf("stickiness = %v, want %v", au.Stickiness, want.Stickiness)
			}

			dictionary := dict.New(pool)
			if err := dictionary.Load(ctx); err != nil {
				t.Fatal(err)
			}
			engine := query.NewEngine(pool, dictionary, registry{site: syntheticSite(loc)})
			engine.SetTrackingMode(privacy.Identified)
			engine.SetClock(func() time.Time { return syntheticNow })
			dr := api.DateRange{From: syntheticDay(want.From), To: syntheticDay(want.To)}

			steps := []api.FunnelStep{
				{Type: api.Page, Match: "exact", Value: "/home"},
				{Type: api.Page, Match: "exact", Value: "/paywall"},
				{Type: api.Event, Match: "exact", Value: "purchase"},
			}
			for _, tc := range []struct {
				countBy api.FunnelRequestCountBy
				want    []int64
			}{
				{api.FunnelCountByVisitors, want.FunnelInstalls},
				{api.FunnelCountBySessions, want.FunnelSessions},
			} {
				countBy := tc.countBy
				got, err := engine.Funnel(ctx, api.FunnelRequest{SiteId: siteID, DateRange: dr, Steps: steps, CountBy: &countBy})
				if err != nil {
					t.Fatal(err)
				}
				var counts []int64
				for _, s := range got.Steps {
					counts = append(counts, s.Count)
				}
				if !reflect.DeepEqual(counts, tc.want) {
					t.Errorf("funnel by %s = %v, want %v", countBy, counts, tc.want)
				}
			}

			ret, err := engine.Retention(ctx, api.RetentionRequest{SiteId: siteID, DateRange: dr, Interval: api.RetentionDay})
			if err != nil {
				t.Fatal(err)
			}
			if len(ret.Cohorts) != len(want.Cohorts) {
				t.Fatalf("cohorts = %d, want %d", len(ret.Cohorts), len(want.Cohorts))
			}
			for i, c := range ret.Cohorts {
				w := want.Cohorts[i]
				if !c.Start.Time.Equal(w.Start) || c.Size != w.Size || !reflect.DeepEqual(c.Retained, w.Retained) {
					t.Errorf("cohort %d = {%s %d %v}, want {%s %d %v}", i, c.Start.Format(time.DateOnly), c.Size, c.Retained, w.Start.Format(time.DateOnly), w.Size, w.Retained)
				}
			}
		})
	}
}

func syntheticSite(loc *time.Location) sites.Site {
	return sites.Site{
		ID: siteID, Kind: sites.KindApp, TZ: loc, Retention: db.Ret90, ProfilesEnabled: true,
		App: &sites.AppConfig{MaxReportAgeDays: 60, HashSecret: secret},
	}
}

// syntheticIngest sends every hit through the adapter and writer and returns
// the database once the writer has drained.
func syntheticIngest(t *testing.T, zone string, loc *time.Location, hits []appproduct.Hit) *pgxpool.Pool {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`insert into orgs(name,slug) values('App Org','app-org')`,
		`insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
		 select 7,id,'appsite00007','App','` + zone + `',30,'app' from orgs`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	site := syntheticSite(loc)
	sess := session.NewMemory(4, syntheticClock{})
	w := writer.New(pool, sess, len(hits)+1, time.Hour, time.Hour)
	w.SetClock(syntheticClock{})
	w.SetTrackingPolicy(privacy.Identified, registry{site: site})
	adapter := appproduct.New(w, sess, dict.New(pool), &drops{}, func() time.Time { return syntheticNow })
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(runCtx) }()
	for _, h := range hits {
		if out := adapter.Accept(ctx, site, h); !out.Accepted() {
			t.Fatalf("hit %+v: %+v", h, out)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("writer: %v", err)
	}
	return pool
}
