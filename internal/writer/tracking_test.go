package writer

import (
	"context"
	"errors"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/privacy"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

type policyRegistry struct{ site sites.Site }

func (r *policyRegistry) Site(id int32) (sites.Site, bool) { return r.site, r.site.ID == id }
func (r *policyRegistry) ByPublicID(id string) (sites.Site, bool) {
	return r.site, r.site.PublicID == id
}
func (r *policyRegistry) All() []sites.Site { return []sites.Site{r.site} }

func TestBufferedIdentityRecheckedOnRetry(t *testing.T) {
	for _, downgrade := range []string{"instance", "project", "missing site"} {
		t.Run(downgrade, func(t *testing.T) {
			registry := &policyRegistry{site: sites.Site{ID: 1, ProfilesEnabled: true}}
			w := New(nil, nil, 1, time.Hour, time.Hour)
			w.SetTrackingPolicy(privacy.Identified, registry)
			if !w.Enqueue(Event{Hit: session.Hit{SiteID: 1}, ReferrerID: 11, ProfileExternalID: "customer", ProfileID: 42, ProfileTraits: map[string]string{"name": "person"}, Props: map[string]string{"email": "person"}}) {
				t.Fatal("enqueue")
			}
			batch := []Event{<-w.ch}
			attempts := 0
			err := w.retryEvents(context.Background(), context.Background(), batch, func(_ context.Context, events []Event) error {
				attempts++
				e := events[0]
				if attempts == 1 {
					if e.ProfileExternalID == "" || e.Props == nil {
						t.Fatal("identified event suppressed")
					}
					switch downgrade {
					case "instance":
						w.SetTrackingPolicy(privacy.Anonymous, registry)
					case "project":
						registry.site.ProfilesEnabled = false
					case "missing site":
						registry.site.ID = 2
					}
					return errors.New("retry after policy change")
				}
				if e.ProfileExternalID != "" || e.ProfileID != 0 || e.ProfileTraits != nil || e.Props != nil || e.ReferrerID != 0 || e.Source != 0 || e.UTMSource != 0 || e.Medium != 0 || e.Campaign != 0 {
					t.Fatalf("buffered identity leaked: %+v", e)
				}
				return nil
			})
			if err != nil || attempts != 2 {
				t.Fatalf("attempts=%d err=%v", attempts, err)
			}
		})
	}
}

func TestWriterDefaultsAnonymous(t *testing.T) {
	w := New(nil, nil, 1, time.Hour, time.Hour)
	events := []Event{{Hit: session.Hit{SiteID: 1}, ProfileExternalID: "forged", ProfileID: 9, ProfileTraits: map[string]string{"name": "secret"}, Props: map[string]string{"identity": "secret"}}}
	if err := w.retryEvents(context.Background(), context.Background(), events, func(_ context.Context, batch []Event) error {
		e := batch[0]
		if e.ProfileExternalID != "" || e.ProfileID != 0 || e.ProfileTraits != nil || e.Props != nil {
			t.Fatalf("default writer retained identity: %+v", e)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBufferedRevenueRecheckedAgainstCurrentProjectCurrency(t *testing.T) {
	registry := &policyRegistry{site: sites.Site{ID: 1, RevenueCurrency: "USD", RevenueCurrencyState: sites.RevenueCurrencyConfigured}}
	w := New(nil, nil, 1, time.Hour, time.Hour)
	w.SetTrackingPolicy(privacy.Anonymous, registry)

	events := []Event{{Hit: session.Hit{SiteID: 1}, RevenueMicros: 4_990_000, RevenueCurrency: "USD"}}
	w.suppressRestrictedIdentity(events)
	if events[0].RevenueMicros == 0 {
		t.Fatal("matching configured revenue was suppressed")
	}

	registry.site.RevenueCurrencyState = sites.RevenueCurrencyConflict
	w.suppressRestrictedIdentity(events)
	if events[0].RevenueMicros != 0 || events[0].RevenueCurrency != "" {
		t.Fatalf("queued revenue survived conflict: %+v", events[0])
	}

	registry.site = sites.Site{ID: 1, RevenueCurrency: "EUR", RevenueCurrencyState: sites.RevenueCurrencyConfigured}
	events = []Event{{Hit: session.Hit{SiteID: 1}, RevenueMicros: 4_990_000, RevenueCurrency: "USD"}}
	w.suppressRestrictedIdentity(events)
	if events[0].RevenueMicros != 0 || events[0].RevenueCurrency != "" {
		t.Fatalf("contradictory queued revenue survived: %+v", events[0])
	}
}

func TestSessionDimensionsRecheckedBeforeFlush(t *testing.T) {
	w := New(nil, nil, 1, time.Hour, time.Hour)
	registry := &policyRegistry{site: sites.Site{ID: 1, ProfilesEnabled: true}}
	w.SetTrackingPolicy(privacy.Identified, registry)
	snapshots := []session.Snapshot{{SiteID: 1, Source: 1, UTMSource: 2, Medium: 3, Campaign: 4}}
	w.suppressRestrictedSessions(snapshots)
	if snapshots[0].Source != 1 {
		t.Fatal("identified attribution lost")
	}
	registry.site.ProfilesEnabled = false
	w.suppressRestrictedSessions(snapshots)
	if s := snapshots[0]; s.Source != 0 || s.UTMSource != 0 || s.Medium != 0 || s.Campaign != 0 {
		t.Fatalf("buffered query dimensions leaked: %+v", s)
	}
}

func TestBufferedDowngradePersistsNoIdentity(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	registry := &policyRegistry{site: sites.Site{ID: 1, ProfilesEnabled: true}}
	w := New(pool, nil, 1, time.Hour, time.Hour)
	w.SetTrackingPolicy(privacy.Identified, registry)
	if !w.Enqueue(Event{Hit: session.Hit{SiteID: 1, Retention: db.Ret30, LocalDay: day, TS: day.Add(time.Minute), VisitorID: 1, PathID: 1, Source: 9, UTMSource: 10, Medium: 11, Campaign: 12}, SessionID: 1, ProfileExternalID: "queued-person", ProfileID: 123, ProfileTraits: map[string]string{"name": "secret"}, Props: map[string]string{"email": "secret"}, ReferrerID: 13}) {
		t.Fatal("enqueue")
	}
	registry.site.ProfilesEnabled = false
	batch := []Event{<-w.ch}
	if err := w.copyEvents(ctx, batch); err != nil {
		t.Fatal(err)
	}
	var profiles, links, props, dimensions int
	if err := pool.QueryRow(ctx, `select (select count(*) from profiles),count(profile_id),count(props),sum(referrer_id+source_id+utm_source_id+medium_id+campaign_id) from events`).Scan(&profiles, &links, &props, &dimensions); err != nil {
		t.Fatal(err)
	}
	if profiles != 0 || links != 0 || props != 0 || dimensions != 0 {
		t.Fatalf("persisted identity: profiles=%d links=%d props=%d dimensions=%d", profiles, links, props, dimensions)
	}
	snapshots := []session.Snapshot{{SiteID: 1, Retention: db.Ret30, LocalDay: day, Start: day.Add(time.Minute), Last: day.Add(time.Minute), SessionID: 1, VisitorID: 1, Source: 9, UTMSource: 10, Medium: 11, Campaign: 12}}
	if err := w.upsertSessions(ctx, snapshots); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select source_id+utm_source_id+medium_id+campaign_id from sessions`).Scan(&dimensions); err != nil {
		t.Fatal(err)
	}
	if dimensions != 0 {
		t.Fatalf("persisted session query dimensions=%d", dimensions)
	}
}
