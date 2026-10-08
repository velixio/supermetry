package writer

import (
	"context"
	"testing"
	"time"

	"panel/internal/db"
	"panel/internal/session"
	"panel/internal/testpg"
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
	w := New(pool, nil, 2, time.Hour, time.Hour, nil)
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
