package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"panel/internal/api"
	"panel/internal/db"
	"panel/internal/platform/authz"
	platformhttp "panel/internal/platform/http"
	"panel/internal/platform/sitesvc"
	"panel/internal/platform/store"
	"panel/internal/testpg"
)

type segmentHarness struct {
	handler http.Handler
	pool    *store.Store
	owner   authz.Principal
	siteID  int32
}

func newSegmentHarness(t *testing.T) segmentHarness {
	t.Helper()
	dbPool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, dbPool); err != nil {
		t.Fatal(err)
	}
	var userID, orgID int64
	if err := dbPool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('segment-owner@example.test','Owner','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := dbPool.QueryRow(ctx, `insert into orgs(name,slug) values ('Segment org','segment-org') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := dbPool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: dbPool}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	owner := authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}
	site, err := sitesvc.New(data, registry).Create(ctx, owner, "Segment site", []string{"segments.example.test"}, "UTC", 30, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbPool.Exec(ctx, `insert into api_keys(id,org_id,name,prefix,hash,scopes,created_by) overriding system value values
		(102,$1,'Segments','segments',decode(repeat('12',32),'hex'),array['segments:write'],$2),
		(103,$1,'Sites','siteskey',decode(repeat('13',32),'hex'),array['sites:write'],$2)`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	authenticator := actorAuthenticator{
		"owner":  owner,
		"editor": {UserID: userID, OrgID: orgID, Role: authz.RoleEditor, SiteIDs: []int32{site.ID}},
		"viewer": {UserID: userID, OrgID: orgID, Role: authz.RoleViewer, SiteIDs: []int32{site.ID}},
		"key":    {KeyID: 99, OrgID: orgID, Role: authz.RoleKey, Scopes: []string{"stats:read"}, SiteIDs: []int32{site.ID}},
		"goalskey": {KeyID: 101, OrgID: orgID, Role: authz.RoleKey,
			Scopes: []string{"goals:write"}, SiteIDs: []int32{site.ID}},
		"noscope": {KeyID: 100, OrgID: orgID, Role: authz.RoleKey,
			Scopes: []string{}, SiteIDs: []int32{site.ID}},
		"segmentskey": {KeyID: 102, OrgID: orgID, Role: authz.RoleKey,
			Scopes: []string{"segments:write"}, SiteIDs: []int32{site.ID}},
		"siteskey": {KeyID: 103, OrgID: orgID, Role: authz.RoleKey,
			Scopes: []string{"sites:write"}, SiteIDs: []int32{site.ID}},
		"outside": {UserID: userID + 100, OrgID: orgID + 100, Role: authz.RoleOwner},
	}
	platform := platformhttp.New(platformhttp.Deps{Authenticator: authenticator, Sites: registry, Store: data})
	return segmentHarness{handler: platform, pool: data, owner: owner, siteID: site.ID}
}

func TestSegmentsCRUDWritesAudit(t *testing.T) {
	h := newSegmentHarness(t)
	ctx := context.Background()
	if _, err := h.pool.Pool.Exec(ctx, `delete from audit_log`); err != nil {
		t.Fatal(err)
	}

	created := segmentRequest(t, h.handler, "owner", http.MethodPost, fmt.Sprintf("/api/private/sites/%d/segments", h.siteID), `{"name":"  Paid traffic  ","filters":[{"dimension":"source","operator":"contains","values":["ads"]}]}`, http.StatusCreated)
	if created.Name != "Paid traffic" || created.SiteID != h.siteID || len(created.Filters) != 1 || created.CreatedBy == nil || *created.CreatedBy != h.owner.UserID {
		t.Fatalf("created segment = %+v", created)
	}

	response := segmentRawRequest(t, h.handler, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/segments", h.siteID), "", http.StatusOK)
	var listed []segmentJSON
	if err := json.NewDecoder(response.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("listed segments = %+v", listed)
	}

	updated := segmentRequest(t, h.handler, "editor", http.MethodPatch, fmt.Sprintf("/api/private/segments/%d", created.ID), `{"name":"Organic","filters":[{"dimension":"prop:plan","operator":"is_not","values":["free"]}]}`, http.StatusOK)
	if updated.Name != "Organic" || len(updated.Filters) != 1 || !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("updated segment = %+v; created = %+v", updated, created)
	}
	segmentRawRequest(t, h.handler, "owner", http.MethodDelete, fmt.Sprintf("/api/private/segments/%d", created.ID), "", http.StatusNoContent)

	rows, err := h.pool.Pool.Query(ctx, `select action from audit_log order by id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	want := []string{"segment.created", "segment.updated", "segment.deleted"}
	if fmt.Sprint(actions) != fmt.Sprint(want) {
		t.Fatalf("audit actions = %v, want %v", actions, want)
	}
}

func TestSegmentValidationAndDuplicateName(t *testing.T) {
	h := newSegmentHarness(t)
	validFilter := `[{"dimension":"country","operator":"is","values":["US"]}]`
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"name":`},
		{name: "blank name", body: `{"name":"   ","filters":` + validFilter + `}`},
		{name: "name too long", body: `{"name":"` + strings.Repeat("x", 81) + `","filters":` + validFilter + `}`},
		{name: "unknown dimension", body: `{"name":"Bad","filters":[{"dimension":"planet","operator":"is","values":["Earth"]}]}`},
		{name: "unknown operator", body: `{"name":"Bad","filters":[{"dimension":"country","operator":"matches","values":["US"]}]}`},
		{name: "empty values", body: `{"name":"Bad","filters":[{"dimension":"country","operator":"is","values":[]}]}`},
		{name: "too many filters", body: `{"name":"Bad","filters":` + repeatedFilters(11) + `}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			segmentRawRequest(t, h.handler, "owner", http.MethodPost, fmt.Sprintf("/api/private/sites/%d/segments", h.siteID), tt.body, http.StatusBadRequest)
		})
	}

	segmentRequest(t, h.handler, "owner", http.MethodPost, fmt.Sprintf("/api/private/sites/%d/segments", h.siteID), `{"name":"Returning","filters":`+validFilter+`}`, http.StatusCreated)
	segmentRawRequest(t, h.handler, "owner", http.MethodPost, fmt.Sprintf("/api/private/sites/%d/segments", h.siteID), `{"name":"returning","filters":`+validFilter+`}`, http.StatusConflict)
}

func TestSegmentAuthorizationIsolationAndPublicScope(t *testing.T) {
	h := newSegmentHarness(t)
	created := segmentRequest(t, h.handler, "owner", http.MethodPost, fmt.Sprintf("/api/private/sites/%d/segments", h.siteID), `{"name":"Saved","filters":[{"dimension":"country","operator":"is","values":["US"]}]}`, http.StatusCreated)

	segmentRawRequest(t, h.handler, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/segments", h.siteID), "", http.StatusOK)
	segmentRawRequest(t, h.handler, "viewer", http.MethodPost, fmt.Sprintf("/api/private/sites/%d/segments", h.siteID), `{"name":"No","filters":[]}`, http.StatusForbidden)
	segmentRawRequest(t, h.handler, "outside", http.MethodPatch, fmt.Sprintf("/api/private/segments/%d", created.ID), `{"name":"No","filters":[]}`, http.StatusNotFound)
	segmentRawRequest(t, h.handler, "key", http.MethodGet, fmt.Sprintf("/api/public/v1/sites/%d/segments", h.siteID), "", http.StatusOK)
	segmentRawRequest(t, h.handler, "noscope", http.MethodGet, fmt.Sprintf("/api/public/v1/sites/%d/segments", h.siteID), "", http.StatusForbidden)
	segmentRawRequest(t, h.handler, "goalskey", http.MethodGet, fmt.Sprintf("/api/public/v1/sites/%d/segments", h.siteID), "", http.StatusForbidden)
	segmentRawRequest(t, h.handler, "key", http.MethodPost, fmt.Sprintf("/api/public/v1/sites/%d/segments", h.siteID), `{"name":"No","filters":[]}`, http.StatusForbidden)
}

func TestSegmentPublicWriteScopesAndAudit(t *testing.T) {
	h := newSegmentHarness(t)
	for _, actor := range []string{"segmentskey", "siteskey"} {
		t.Run(actor, func(t *testing.T) {
			created := segmentRequest(t, h.handler, actor, http.MethodPost, fmt.Sprintf("/api/public/v1/sites/%d/segments", h.siteID), `{"name":"Public `+actor+`","filters":[{"dimension":"country","operator":"is","values":["US"]}]}`, http.StatusCreated)
			segmentRequest(t, h.handler, actor, http.MethodPatch, fmt.Sprintf("/api/public/v1/segments/%d", created.ID), `{"name":"Updated `+actor+`","filters":[{"dimension":"country","operator":"is","values":["US"]}]}`, http.StatusOK)
			segmentRawRequest(t, h.handler, actor, http.MethodDelete, fmt.Sprintf("/api/public/v1/segments/%d", created.ID), "", http.StatusNoContent)
		})
	}
	segmentRawRequest(t, h.handler, "key", http.MethodPost, fmt.Sprintf("/api/public/v1/sites/%d/segments", h.siteID), `{"name":"No","filters":[]}`, http.StatusForbidden)
	segmentRawRequest(t, h.handler, "segmentskey", http.MethodPost, fmt.Sprintf("/api/public/v1/sites/%d/segments", h.siteID+9999), `{"name":"No","filters":[]}`, http.StatusNotFound)

	var audits int
	if err := h.pool.Pool.QueryRow(context.Background(), `select count(*) from audit_log where action like 'segment.%' and actor_key_id is not null`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 6 {
		t.Fatalf("API-key audit rows=%d, want 6", audits)
	}
}

type segmentJSON struct {
	ID        int64        `json:"id"`
	SiteID    int32        `json:"site_id"`
	Name      string       `json:"name"`
	Filters   []api.Filter `json:"filters"`
	CreatedBy *int64       `json:"created_by"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

func segmentRequest(t *testing.T, handler http.Handler, actor, method, path, body string, want int) segmentJSON {
	t.Helper()
	response := segmentRawRequest(t, handler, actor, method, path, body, want)
	var segment segmentJSON
	if err := json.NewDecoder(response.Body).Decode(&segment); err != nil {
		t.Fatal(err)
	}
	return segment
}

func segmentRawRequest(t *testing.T, handler http.Handler, actor, method, path, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Test-Actor", actor)
	request.Header.Set("X-CSRF-Token", "token")
	request.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("%s %s as %s: status=%d body=%s, want %d", method, path, actor, response.Code, response.Body.String(), want)
	}
	return response
}

func repeatedFilters(count int) string {
	items := make([]string, count)
	for i := range items {
		items[i] = `{"dimension":"country","operator":"is","values":["US"]}`
	}
	return "[" + strings.Join(items, ",") + "]"
}
