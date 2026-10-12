package session

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestActivityFeedIsCappedAndIsolatedPerSite(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	memory := NewMemory(2, nil)
	for i := 0; i < 150; i++ {
		memory.RecordActivity(Activity{SiteID: 1, Timestamp: now.Add(time.Duration(i) * time.Millisecond), VisitorID: int64(i), Kind: 0, PathID: 1})
	}
	memory.RecordActivity(Activity{SiteID: 2, Timestamp: now, VisitorID: 999, Kind: 0, PathID: 2})

	first := memory.activity.site(1, false)
	second := memory.activity.site(2, false)
	if first == nil || first.length != activityFeedLimit {
		t.Fatalf("site 1 feed length=%v, want %d", first, activityFeedLimit)
	}
	if second == nil || second.length != 1 {
		t.Fatalf("site 2 feed length=%v, want 1", second)
	}
	firstSnapshot := memory.RealtimeSnapshot(1, now.Add(time.Second))
	secondSnapshot := memory.RealtimeSnapshot(2, now.Add(time.Second))
	if firstSnapshot.Pageviews != 150 || len(firstSnapshot.RecentActivity) != 50 {
		t.Fatalf("site 1 totals/feed=%d/%d", firstSnapshot.Pageviews, len(firstSnapshot.RecentActivity))
	}
	if secondSnapshot.Pageviews != 1 || len(secondSnapshot.RecentActivity) != 1 || secondSnapshot.RecentActivity[0].PathID != 2 {
		t.Fatalf("site 2 snapshot=%+v", secondSnapshot)
	}
}

func TestRecentActivityFeedTypeContainsNoIdentityOrNetworkFields(t *testing.T) {
	typeOfFeedEntry := reflect.TypeOf(RecentActivity{})
	for i := 0; i < typeOfFeedEntry.NumField(); i++ {
		name := strings.ToLower(typeOfFeedEntry.Field(i).Name)
		for _, forbidden := range []string{"visitor", "session", "profile", "ip", "useragent", "ua"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("feed field %q contains forbidden identity/network marker %q", name, forbidden)
			}
		}
	}
}

func TestExpireDropsIdleActivitySitesAndFreesSlots(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	memory := NewMemory(2, nil)
	for id := int32(1); id <= activitySiteLimit; id++ {
		memory.RecordActivity(Activity{SiteID: id, Timestamp: now, VisitorID: 1, Kind: 0, PathID: 1})
	}
	memory.RecordActivity(Activity{SiteID: activitySiteLimit + 1, Timestamp: now, VisitorID: 1, Kind: 0, PathID: 1})
	if memory.activity.site(activitySiteLimit+1, false) != nil {
		t.Fatal("site beyond the limit was tracked")
	}
	if got := memory.activity.total.Load(); got != activitySiteLimit {
		t.Fatalf("tracked sites=%d, want %d", got, activitySiteLimit)
	}

	later := now.Add(IdleTimeout + time.Minute)
	memory.RecordActivity(Activity{SiteID: 1, Timestamp: later, VisitorID: 2, Kind: 0, PathID: 1})
	memory.Expire(later)
	if got := memory.activity.total.Load(); got != 1 {
		t.Fatalf("tracked sites after expire=%d, want 1", got)
	}
	if memory.activity.site(2, false) != nil {
		t.Fatal("idle site 2 still tracked")
	}
	memory.RecordActivity(Activity{SiteID: activitySiteLimit + 1, Timestamp: later, VisitorID: 1, Kind: 0, PathID: 1})
	if memory.RealtimeSnapshot(activitySiteLimit+1, later).Pageviews != 1 {
		t.Fatal("freed slot not reusable")
	}
	if memory.RealtimeSnapshot(1, later).Pageviews != 1 {
		t.Fatal("active site lost its activity")
	}
}

func TestOutOfOrderActivityDoesNotWipeNewerMinute(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 30, 0, 0, time.UTC)
	memory := NewMemory(2, nil)
	memory.RecordActivity(Activity{SiteID: 1, Timestamp: now, VisitorID: 1, Kind: 0, PathID: 1})
	// Same ring slot, thirty minutes earlier (e.g. a delayed or skewed hit).
	memory.RecordActivity(Activity{SiteID: 1, Timestamp: now.Add(-realtimeMinutes * time.Minute), VisitorID: 2, Kind: 0, PathID: 1})
	snapshot := memory.RealtimeSnapshot(1, now)
	if snapshot.Pageviews != 1 || snapshot.VisitorsPerMinute[realtimeMinutes-1].Visitors != 1 {
		t.Fatalf("current minute wiped: pageviews=%d visitors=%d", snapshot.Pageviews, snapshot.VisitorsPerMinute[realtimeMinutes-1].Visitors)
	}
}

func TestActiveVisitorsSinceUsesExactEdgesAndDistinctVisitors(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 1, 0, time.UTC)
	memory := NewMemory(2, nil)
	activities := []Activity{
		{SiteID: 1, Timestamp: now.Add(-4*time.Minute - 59*time.Second), VisitorID: 1, Kind: 0},
		{SiteID: 1, Timestamp: now.Add(-2 * time.Minute), VisitorID: 1, Kind: 0},
		{SiteID: 1, Timestamp: now.Add(-time.Minute), VisitorID: 1, Kind: 1},
		{SiteID: 1, Timestamp: now.Add(-5 * time.Minute), VisitorID: 2, Kind: 0},
		{SiteID: 1, Timestamp: now.Add(-5*time.Minute - time.Second), VisitorID: 3, Kind: 0},
		{SiteID: 2, Timestamp: now, VisitorID: 4, Kind: 0},
		{SiteID: 1, Timestamp: now.Add(time.Second), VisitorID: 5, Kind: 0},
	}
	for _, activity := range activities {
		memory.RecordActivity(activity)
	}

	if got := memory.ActiveVisitorsSince(1, now.Add(-5*time.Minute), now); got != 2 {
		t.Fatalf("ActiveVisitorsSince=%d, want 2", got)
	}
}

func TestActiveVisitorsSinceCountsOnlyAcceptedActivityKinds(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	memory := NewMemory(1, nil)
	memory.RecordActivity(Activity{SiteID: 1, Timestamp: now, VisitorID: 1, Kind: 2})

	if got := memory.ActiveVisitorsSince(1, now.Add(-5*time.Minute), now); got != 0 {
		t.Fatalf("ActiveVisitorsSince=%d, want 0 for engagement activity", got)
	}
}

func TestActiveVisitorsSinceFutureHitDoesNotHideValidHit(t *testing.T) {
	memory := NewMemory(1, nil)
	now := time.Date(2026, 10, 12, 12, 0, 30, 0, time.UTC)
	memory.RecordActivity(Activity{SiteID: 1, VisitorID: 7, Timestamp: now.Add(-10 * time.Second)})
	memory.RecordActivity(Activity{SiteID: 1, VisitorID: 7, Timestamp: now.Add(10 * time.Second)})

	if got := memory.ActiveVisitorsSince(1, now.Add(-5*time.Minute), now); got != 1 {
		t.Fatalf("ActiveVisitorsSince=%d, want 1", got)
	}
}
