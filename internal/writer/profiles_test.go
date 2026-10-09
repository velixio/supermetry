package writer

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/privacy"
	"supermetry/internal/profiletombstone"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

func TestCopyEventsUpsertsOneProfileAndLinksBatch(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name, slug) values ('Writer profiles', 'writer-profiles') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id, public_id, name, allowed_hosts, timezone, retention_class, settings)
		values ($1, 'writerprof01', 'Writer profiles', '{example.com}', 'UTC', 30, '{"profiles_enabled":true}')
		returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}

	events := []Event{
		{
			Hit: session.Hit{
				SiteID: siteID, Retention: db.Ret30, LocalDay: day, TS: day.Add(time.Minute),
				VisitorID: 1, Kind: 1, NameID: 1, PathID: 1,
			},
			SessionID: 1, ProfileExternalID: "customer-1",
			ProfileTraits: map[string]string{"plan": "free", "region": "apac"},
		},
		{
			Hit: session.Hit{
				SiteID: siteID, Retention: db.Ret30, LocalDay: day, TS: day.Add(2 * time.Minute),
				VisitorID: 1, Kind: 1, NameID: 1, PathID: 1,
			},
			SessionID: 1, ProfileExternalID: "customer-1",
			ProfileTraits: map[string]string{"plan": "pro"},
		},
	}
	w := New(pool, nil, 2, time.Hour, time.Hour)
	w.SetTrackingPolicy(privacy.Identified, &policyRegistry{site: sites.Site{ID: siteID, ProfilesEnabled: true}})
	if err := w.copyEvents(ctx, events); err != nil {
		t.Fatal(err)
	}

	var profiles int
	var profileID int64
	var traits string
	var firstSeen, lastSeen time.Time
	if err := pool.QueryRow(ctx, `
		select count(*), min(id), min(traits::text), min(first_seen_at), max(last_seen_at)
		from profiles where site_id = $1`, siteID).Scan(&profiles, &profileID, &traits, &firstSeen, &lastSeen); err != nil {
		t.Fatal(err)
	}
	if profiles != 1 || profileID == 0 || traits != `{"plan": "pro", "region": "apac"}` {
		t.Fatalf("profiles=%d id=%d traits=%s", profiles, profileID, traits)
	}
	if !firstSeen.Equal(day.Add(time.Minute)) || !lastSeen.Equal(day.Add(2*time.Minute)) {
		t.Fatalf("seen range=%s..%s", firstSeen, lastSeen)
	}
	var linked, distinct int
	if err := pool.QueryRow(ctx, `
		select count(profile_id), count(distinct profile_id)
		from events where site_id = $1`, siteID).Scan(&linked, &distinct); err != nil {
		t.Fatal(err)
	}
	if linked != 2 || distinct != 1 || events[0].ProfileID != profileID || events[1].ProfileID != profileID {
		t.Fatalf("linked=%d distinct=%d event profile IDs=%d/%d, want 2/1/%d", linked, distinct, events[0].ProfileID, events[1].ProfileID, profileID)
	}
}

func TestBufferedProfileEventStaysAnonymousAfterErasureAndRestart(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
		create table events_r30_20261008 partition of events_r30
		for values from ('2026-10-08T00:00:00Z') to ('2026-10-09T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Erasure','erasure') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'erasure00001','Erasure','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}

	tombstones := profiletombstone.New(pool, nil)
	if err := tombstones.Remember(ctx, siteID, "customer-1"); err != nil {
		t.Fatal(err)
	}
	restarted := profiletombstone.New(pool, nil)
	if err := restarted.Load(ctx); err != nil {
		t.Fatal(err)
	}
	events := []Event{{
		Hit:       session.Hit{SiteID: siteID, Retention: db.Ret30, LocalDay: day, TS: day.Add(time.Minute), VisitorID: 1, Kind: 1},
		SessionID: 1, ProfileExternalID: "customer-1", ProfileTraits: map[string]string{"plan": "pro"},
	}}
	w := New(pool, nil, 1, time.Hour, time.Hour)
	w.SetTrackingPolicy(privacy.Identified, &policyRegistry{site: sites.Site{ID: siteID, ProfilesEnabled: true}})
	w.SetProfileTombstones(restarted)
	if err := w.copyEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	var profiles, linked int
	if err := pool.QueryRow(ctx, `select (select count(*) from profiles where site_id=$1), count(profile_id) from events where site_id=$1`, siteID).Scan(&profiles, &linked); err != nil {
		t.Fatal(err)
	}
	if profiles != 0 || linked != 0 || events[0].ProfileExternalID != "" || events[0].ProfileTraits != nil {
		t.Fatalf("profiles=%d linked=%d event=%+v, want anonymous", profiles, linked, events[0])
	}
}
