package query_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

func TestSessionsCombinesHotAndClosedNewestFirst(t *testing.T) {
	pool, engine := setupTiny(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
insert into orgs(id,name,slug) overriding system value values (1,'Sessions','sessions-query');
insert into sites(id,org_id,public_id,name,timezone,retention_class,settings) overriding system value
values (1,1,'sessionquery','Sessions','UTC',30,'{"profiles_enabled":true}');
insert into profiles(id,site_id,external_id,first_seen_at,last_seen_at) overriding system value
values (1,1,'ada-42','2026-10-07 09:00Z','2026-10-08 08:01Z');
update events set profile_id=1 where site_id=1 and session_id in (201,301);
update sessions_closed set profile_id=1 where site_id=1 and session_id=201`); err != nil {
		t.Fatal(err)
	}

	limit := api.Limit(3)
	response, err := engine.Sessions(ctx, 1, api.ListSessionsParams{
		From:  openapi_types.Date{Time: day("2026-10-06")},
		To:    openapi_types.Date{Time: day("2026-10-08")},
		Limit: &limit,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sessionIDs(response.Data), []string{"302", "301", "202"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("session IDs = %v, want %v", got, want)
	}
	if response.NextCursor == nil {
		t.Fatal("next_cursor is nil")
	}
	if response.Total != 7 || response.TotalCapped {
		t.Fatalf("total=%d capped=%t, want 7/false", response.Total, response.TotalCapped)
	}
	if response.Data[1].ExternalId == nil || *response.Data[1].ExternalId != "ada-42" {
		t.Fatalf("hot profile external_id = %v", response.Data[1].ExternalId)
	}
	if response.Data[2].EntryPage != "/blog/post" || response.Data[2].City != "Bengaluru" {
		t.Fatalf("closed dimensions = %+v", response.Data[2])
	}

	cursor := api.Cursor(*response.NextCursor)
	second, err := engine.Sessions(ctx, 1, api.ListSessionsParams{
		From:   openapi_types.Date{Time: day("2026-10-06")},
		To:     openapi_types.Date{Time: day("2026-10-08")},
		Limit:  &limit,
		Cursor: &cursor,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sessionIDs(second.Data), []string{"201", "103", "102"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second page IDs = %v, want %v", got, want)
	}
	if second.Total != response.Total || second.TotalCapped {
		t.Fatalf("second total=%d capped=%t, want %d/false", second.Total, second.TotalCapped, response.Total)
	}
}

func TestSessionsValidatesAndFilters(t *testing.T) {
	_, engine := setupTiny(t)
	ctx := context.Background()
	filters := []api.Filter{{Dimension: "country", Operator: api.Is, Values: []string{"US"}}}
	response, err := engine.Sessions(ctx, 1, api.ListSessionsParams{
		From: openapi_types.Date{Time: day("2026-10-06")},
		To:   openapi_types.Date{Time: day("2026-10-08")},
	}, filters)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sessionIDs(response.Data), []string{"301", "201", "103", "101"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered IDs = %v, want %v", got, want)
	}
	if response.Total != 4 || response.TotalCapped {
		t.Fatalf("filtered total=%d capped=%t, want 4/false", response.Total, response.TotalCapped)
	}

	_, err = engine.Sessions(ctx, 1, api.ListSessionsParams{
		From: openapi_types.Date{Time: day("2026-10-08")},
		To:   openapi_types.Date{Time: day("2026-10-06")},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "from must not be after to") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestSessionsHasReplayFilter(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, day("2026-10-08"), day("2026-10-09")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day,pageviews)
		values (1,30,301,1,'2026-10-08 08:00Z','2026-10-08 08:01Z','2026-10-08',1),
		       (1,30,302,2,'2026-10-08 09:00Z','2026-10-08 09:01Z','2026-10-08',1);
		insert into replay_sessions(site_id,session_id,started_at,last_seen_at,day,bytes,pages,blob_key)
		values (1,301,'2026-10-08 08:00Z','2026-10-08 08:01Z','2026-10-08',10,1,'replay/1/2026-10-08/301.gz')`); err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	engine := identifiedEngine(pool, dictionary, sitesRegistry{{ID: 1, PublicID: "replay", TZ: time.UTC, Retention: db.Ret30}})
	hasReplay := true
	response, err := engine.Sessions(ctx, 1, api.ListSessionsParams{
		From:      openapi_types.Date{Time: day("2026-10-06")},
		To:        openapi_types.Date{Time: day("2026-10-08")},
		HasReplay: &hasReplay,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sessionIDs(response.Data), []string{"301"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("session IDs = %v, want %v", got, want)
	}
	if !response.Data[0].HasReplay {
		t.Fatalf("session = %+v, want has_replay", response.Data[0])
	}
}

func TestSessionTimelineIsHotOnlyAndPrivacySafe(t *testing.T) {
	pool, engine := setupTiny(t)
	if _, err := pool.Exec(context.Background(), `
insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id)
values (1,30,'2026-10-08','2026-10-08 08:01:30Z',3001,301,2,2)`); err != nil {
		t.Fatal(err)
	}
	detail, err := engine.Session(context.Background(), 1, 301)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Session.Id != "301" || len(detail.Timeline) != 2 {
		t.Fatalf("detail = %+v", detail)
	}
	if detail.Timeline[0].Kind != api.SessionPageview || detail.Timeline[0].Page != "/" {
		t.Fatalf("first timeline item = %+v", detail.Timeline[0])
	}

	if _, err := pool.Exec(context.Background(), `
delete from events where site_id=1 and session_id=201;
delete from sessions where site_id=1 and session_id=201`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Session(context.Background(), 1, 201); err == nil || !strings.Contains(err.Error(), "raw retention") {
		t.Fatalf("closed session error = %v", err)
	}
	if _, err := engine.Session(context.Background(), 1, 999); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown session error = %v", err)
	}
}

func sessionIDs(sessions []api.SessionSummary) []string {
	ids := make([]string, len(sessions))
	for index, session := range sessions {
		ids[index] = session.Id
	}
	return ids
}

type sitesRegistry []sites.Site

func (r sitesRegistry) ByPublicID(id string) (sites.Site, bool) {
	for _, site := range r {
		if site.PublicID == id {
			return site, true
		}
	}
	return sites.Site{}, false
}

func (r sitesRegistry) Site(id int32) (sites.Site, bool) {
	for _, site := range r {
		if site.ID == id {
			return site, true
		}
	}
	return sites.Site{}, false
}

func (r sitesRegistry) All() []sites.Site { return r }

func TestSessionsHideProfilesAndOtherSites(t *testing.T) {
	pool, _ := setupTiny(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
insert into orgs(id,name,slug) overriding system value values (1,'Sessions','sessions-privacy');
insert into sites(id,org_id,public_id,name,timezone,retention_class,settings) overriding system value
values (1,1,'sessionquery','Sessions','UTC',30,'{}');
insert into profiles(id,site_id,external_id,first_seen_at,last_seen_at) overriding system value
values (1,1,'ada-42','2026-10-07 09:00Z','2026-10-08 08:01Z');
update events set profile_id=1 where site_id=1 and session_id in (201,301);
update sessions_closed set profile_id=1 where site_id=1 and session_id=201`); err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	engine := identifiedEngine(pool, dictionary, sitesRegistry{
		{ID: 1, PublicID: "tiny", TZ: time.UTC, Retention: db.Ret30},
		{ID: 2, PublicID: "other", TZ: time.UTC, Retention: db.Ret30, ProfilesEnabled: true},
	})

	response, err := engine.Sessions(ctx, 1, api.ListSessionsParams{
		From: openapi_types.Date{Time: day("2026-10-06")},
		To:   openapi_types.Date{Time: day("2026-10-08")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Data) == 0 {
		t.Fatal("no sessions returned")
	}
	for _, item := range response.Data {
		if item.ExternalId != nil {
			t.Fatalf("session %s exposes external_id %q with profiles disabled", item.Id, *item.ExternalId)
		}
	}
	detail, err := engine.Session(ctx, 1, 301)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Session.ExternalId != nil {
		t.Fatalf("detail exposes external_id %q with profiles disabled", *detail.Session.ExternalId)
	}

	for _, id := range []int64{301, 201} {
		if _, err := engine.Session(ctx, 2, id); !errors.Is(err, api.ErrSessionNotFound) {
			t.Fatalf("site 2 session %d error = %v, want not found", id, err)
		}
	}
	other, err := engine.Sessions(ctx, 2, api.ListSessionsParams{
		From: openapi_types.Date{Time: day("2026-10-06")},
		To:   openapi_types.Date{Time: day("2026-10-08")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Data) != 0 {
		t.Fatalf("site 2 sees %d sessions of site 1", len(other.Data))
	}
}
