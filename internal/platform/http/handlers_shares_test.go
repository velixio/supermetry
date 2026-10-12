package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/db"
	"supermetry/internal/platform/authn"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/shares"
	"supermetry/internal/platform/store"
	coresites "supermetry/internal/sites"
	"supermetry/internal/testpg"
)

type recordingShareQuery struct {
	requests []api.QueryRequest
}

func (q *recordingShareQuery) Query(_ context.Context, request api.QueryRequest) (api.QueryResponse, error) {
	q.requests = append(q.requests, request)
	return api.QueryResponse{Columns: []string{}, Rows: []map[string]any{}, Totals: map[string]float64{"visitors": 7}, Meta: api.QueryMeta{SourcesUsed: []string{}}}, nil
}

func TestShareExpiryHTTPManagementAndViewer(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('share-owner@example.test','Owner','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Shares','shares') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'sharesite001','Share site','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	service := shares.New(data, []byte("0123456789abcdef0123456789abcdef"))
	owner := authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}
	registry := siteRegistry{siteID: coresites.Site{ID: siteID, OrgID: orgID, PublicID: "sharesite001", TZ: time.UTC}}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"owner": owner}, Sites: registry, Store: data,
		Shares: service, Query: routeQuery{},
	})

	created := requestAs(t, handler, "owner", http.MethodPost, "/api/private/sites/"+stringID(siteID)+"/shares", `{"password":"correct horse battery staple","expires_in_days":7,"config":{"sections":["kpis","chart"],"date":{"mode":"rolling","days":30,"max_days":30},"theme":"light","embed":{"enabled":false}}}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var response struct {
		Slug  string `json:"slug"`
		Share struct {
			ID        int64         `json:"id"`
			ExpiresAt *time.Time    `json:"expires_at"`
			Config    shares.Config `json:"config"`
		} `json:"share"`
	}
	if err := json.NewDecoder(created.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Slug == "" || response.Share.ID == 0 || response.Share.ExpiresAt == nil || response.Share.Config.Theme != shares.ThemeLight {
		t.Fatalf("created share = %+v", response)
	}

	changed := requestAs(t, handler, "owner", http.MethodPatch, "/api/private/sites/"+stringID(siteID)+"/shares/"+stringID64(response.Share.ID), `{"expires_in_days":30}`)
	if changed.Code != http.StatusNoContent {
		t.Fatalf("patch status=%d body=%s", changed.Code, changed.Body.String())
	}

	listed := requestAs(t, handler, "owner", http.MethodGet, "/api/private/sites/"+stringID(siteID)+"/shares", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	var list struct {
		Data []struct {
			ExpiresAt *time.Time    `json:"expires_at"`
			Config    shares.Config `json:"config"`
		} `json:"data"`
	}
	if err := json.NewDecoder(listed.Body).Decode(&list); err != nil || len(list.Data) != 1 || list.Data[0].ExpiresAt == nil || fmt.Sprint(list.Data[0].Config.Sections) != "[kpis chart]" {
		t.Fatalf("listed shares = %+v error=%v", list, err)
	}

	cookie, err := service.Open(ctx, response.Slug, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update shared_links set expires_at=now()-interval '1 second' where id=$1`, response.Share.ID); err != nil {
		t.Fatal(err)
	}
	viewer := platformhttp.New(platformhttp.Deps{
		Authenticator: platformhttp.NewAuthenticator(nil, nil, service, nil), Sites: registry,
		Shares: service, Query: routeQuery{},
	})
	expiredRequest := httptest.NewRequest(http.MethodGet, "/api/private/share/"+response.Slug+"/stats/summary?period=7d", nil)
	expiredRequest.AddCookie(&http.Cookie{Name: authn.ShareCookieName, Value: cookie})
	expired := httptest.NewRecorder()
	viewer.ServeHTTP(expired, expiredRequest)
	missing := httptest.NewRecorder()
	viewer.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/private/share/shr_missing/stats/summary?period=7d", nil))
	if expired.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || expired.Body.String() != missing.Body.String() {
		t.Fatalf("expired=(%d,%q) missing=(%d,%q)", expired.Code, expired.Body.String(), missing.Code, missing.Body.String())
	}
}

func TestShareViewerUsesOnlyPersistedSectionsFiltersAndRange(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('configured-share@example.test','Owner','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Configured shares','configured-shares') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'configshare1','Configured','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	service := shares.New(data, []byte("0123456789abcdef0123456789abcdef"))
	owner := authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}
	slug, _, err := service.CreateConfigured(ctx, owner, siteID, "", nil, shares.Config{
		Sections: []shares.Section{shares.SectionKPIs, shares.SectionPages},
		Filters:  []api.Filter{{Dimension: "country", Operator: api.Is, Values: []string{"IN"}}},
		Date:     shares.DateConfig{Mode: shares.DateFixed, From: "2026-10-01", To: "2026-10-07", MaxDays: 7},
		Theme:    shares.ThemeDark,
	})
	if err != nil {
		t.Fatal(err)
	}
	queries := &recordingShareQuery{}
	handler := platformhttp.New(platformhttp.Deps{
		Shares: service, Store: data, Query: queries, Now: func() time.Time { return time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC) },
		Sites: siteRegistry{siteID: {ID: siteID, OrgID: orgID, PublicID: "configshare1", TZ: time.UTC}},
	})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/private/share/"+slug+"/stats/summary?period=365d&filter=country:is:US", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("summary status=%d body=%s", response.Code, response.Body.String())
	}
	var view struct {
		Sections []shares.Section  `json:"sections"`
		Theme    shares.Theme      `json:"theme"`
		Data     api.QueryResponse `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(view.Sections) != "[kpis pages]" || view.Theme != shares.ThemeDark || view.Data.Totals["visitors"] != 7 {
		t.Fatalf("share view = %+v", view)
	}
	if len(queries.requests) != 1 {
		t.Fatalf("query count = %d, want 1", len(queries.requests))
	}
	wantFrom, wantTo := openapi_types.Date{Time: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}, openapi_types.Date{Time: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)}
	got := queries.requests[0]
	if got.SiteId != siteID || got.DateRange == nil || got.DateRange.From != wantFrom || got.DateRange.To != wantTo || got.Period != nil || got.Filters == nil || len(*got.Filters) != 1 || (*got.Filters)[0].Values[0] != "IN" {
		t.Fatalf("locked query = %+v", got)
	}

	hidden := httptest.NewRecorder()
	handler.ServeHTTP(hidden, httptest.NewRequest(http.MethodPost, "/api/private/share/"+slug+"/query", strings.NewReader(`{"section":"sources"}`)))
	if hidden.Code != http.StatusForbidden || len(queries.requests) != 1 {
		t.Fatalf("hidden section status=%d queries=%d body=%s", hidden.Code, len(queries.requests), hidden.Body.String())
	}
	visitorOverrides := []string{
		`{"section":"pages","filters":[{"dimension":"country","operator":"is","values":["US"]}]}`,
		`{"section":"pages","segment_id":42}`,
		`{"section":"pages","date":{"mode":"rolling","days":366,"max_days":366}}`,
		`{"section":"pages","theme":"light"}`,
		`{"section":"pages","embed":{"enabled":true,"confirm_wildcard":true}}`,
	}
	for _, body := range visitorOverrides {
		visible := httptest.NewRecorder()
		handler.ServeHTTP(visible, httptest.NewRequest(http.MethodPost, "/api/private/share/"+slug+"/query", strings.NewReader(body)))
		if visible.Code != http.StatusBadRequest || len(queries.requests) != 1 {
			t.Fatalf("visitor override body=%s status=%d queries=%d response=%s", body, visible.Code, len(queries.requests), visible.Body.String())
		}
	}
}

func stringID(value int32) string { return stringID64(int64(value)) }

func stringID64(value int64) string {
	return strconv.FormatInt(value, 10)
}
