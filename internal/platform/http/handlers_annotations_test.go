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

	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/db"
	"panel/internal/platform/authz"
	platformhttp "panel/internal/platform/http"
	"panel/internal/platform/sitesvc"
	"panel/internal/platform/store"
	"panel/internal/testpg"
)

type invalidationCounter struct{ count int }

func (c *invalidationCounter) Invalidate() error {
	c.count++
	return nil
}

type annotationFixture struct {
	handler       http.Handler
	pool          *pgxpool.Pool
	siteID        int32
	foreignSite   int32
	userID        int64
	invalidations *invalidationCounter
}

func newAnnotationFixture(t *testing.T) annotationFixture {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, orgID, foreignOrgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('annotations@example.test','Annotator','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Annotations','annotations') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Foreign','foreign-annotations') returning id`).Scan(&foreignOrgID); err != nil {
		t.Fatal(err)
	}
	var siteID, foreignSite int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'annotate0001','Site','Asia/Kathmandu',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'annotate0002','Foreign','UTC',30) returning id`, foreignOrgID).Scan(&foreignSite); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into api_keys(id,org_id,name,prefix,hash,scopes,created_by) overriding system value values
		(13,$1,'Annotations','annotate',decode(repeat('13',32),'hex'),array['annotations:write'],$2),
		(14,$1,'Sites','siteskey',decode(repeat('14',32),'hex'),array['sites:write'],$2)`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	invalidations := &invalidationCounter{}
	authenticator := actorAuthenticator{
		"owner":          {UserID: userID, OrgID: orgID, Role: authz.RoleOwner},
		"editor":         {UserID: userID, OrgID: orgID, Role: authz.RoleEditor},
		"viewer":         {UserID: userID, OrgID: orgID, Role: authz.RoleViewer},
		"key":            {KeyID: 11, OrgID: orgID, Role: authz.RoleKey, Scopes: []string{"stats:read"}},
		"key-no-scope":   {KeyID: 12, OrgID: orgID, Role: authz.RoleKey},
		"annotation-key": {KeyID: 13, OrgID: orgID, Role: authz.RoleKey, Scopes: []string{"annotations:write"}},
		"sites-key":      {KeyID: 14, OrgID: orgID, Role: authz.RoleKey, Scopes: []string{"sites:write"}},
	}
	return annotationFixture{
		handler: platformhttp.New(platformhttp.Deps{
			Authenticator: authenticator,
			Sites:         registry,
			Store:         data,
			Definitions:   invalidations,
		}),
		pool: pool, siteID: siteID, foreignSite: foreignSite, userID: userID, invalidations: invalidations,
	}
}

func (f annotationFixture) request(t *testing.T, actor, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if actor != "" {
		request.Header.Set("X-Test-Actor", actor)
	}
	if method != http.MethodGet {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", "token")
		request.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "token"})
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func TestAnnotationsCRUDAndAudit(t *testing.T) {
	f := newAnnotationFixture(t)
	created := f.request(t, "editor", http.MethodPost, fmt.Sprintf("/api/private/sites/%d/annotations", f.siteID), `{"day":"2026-10-08","text":" Release day "}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("POST status=%d body=%s", created.Code, created.Body.String())
	}
	var annotation struct {
		ID        int64     `json:"id"`
		SiteID    int32     `json:"site_id"`
		Day       string    `json:"day"`
		Text      string    `json:"text"`
		CreatedBy *int64    `json:"created_by"`
		CreatedAt time.Time `json:"created_at"`
	}
	if err := json.NewDecoder(created.Body).Decode(&annotation); err != nil {
		t.Fatal(err)
	}
	if annotation.ID == 0 || annotation.SiteID != f.siteID || annotation.Day != "2026-10-08" || annotation.Text != "Release day" || annotation.CreatedBy == nil || *annotation.CreatedBy != f.userID || annotation.CreatedAt.IsZero() {
		t.Fatalf("created=%+v", annotation)
	}

	listed := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/annotations?from=2026-10-01&to=2026-10-31", f.siteID), "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"text":"Release day"`) {
		t.Fatalf("GET status=%d body=%s", listed.Code, listed.Body.String())
	}

	updated := f.request(t, "editor", http.MethodPatch, fmt.Sprintf("/api/private/annotations/%d", annotation.ID), `{"day":"2026-10-09","text":"Shipped"}`)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"day":"2026-10-09"`) || !strings.Contains(updated.Body.String(), `"text":"Shipped"`) {
		t.Fatalf("PATCH status=%d body=%s", updated.Code, updated.Body.String())
	}

	deleted := f.request(t, "editor", http.MethodDelete, fmt.Sprintf("/api/private/annotations/%d", annotation.ID), "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if f.invalidations.count != 3 {
		t.Fatalf("invalidations=%d, want 3", f.invalidations.count)
	}

	var auditCount int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from audit_log where action like 'annotation.%'`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 3 {
		t.Fatalf("audit rows=%d, want 3", auditCount)
	}
	empty := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/annotations?from=2026-10-01&to=2026-10-31", f.siteID), "")
	if empty.Code != http.StatusOK || strings.TrimSpace(empty.Body.String()) != "[]" {
		t.Fatalf("GET after DELETE status=%d body=%s", empty.Code, empty.Body.String())
	}
}

func TestAnnotationsAuthorization(t *testing.T) {
	f := newAnnotationFixture(t)
	privatePath := fmt.Sprintf("/api/private/sites/%d/annotations", f.siteID)
	publicPath := fmt.Sprintf("/api/public/v1/sites/%d/annotations", f.siteID)
	tests := []struct {
		name, actor, method, path, body string
		want                            int
	}{
		{name: "viewer reads private", actor: "viewer", method: http.MethodGet, path: privatePath + "?from=2026-10-01&to=2026-10-31", want: http.StatusOK},
		{name: "viewer cannot create", actor: "viewer", method: http.MethodPost, path: privatePath, body: `{"day":"2026-10-08","text":"No"}`, want: http.StatusForbidden},
		{name: "stats key reads public", actor: "key", method: http.MethodGet, path: publicPath + "?from=2026-10-01&to=2026-10-31", want: http.StatusOK},
		{name: "missing scope is forbidden", actor: "key-no-scope", method: http.MethodGet, path: publicPath + "?from=2026-10-01&to=2026-10-31", want: http.StatusForbidden},
		{name: "foreign site is hidden", actor: "key", method: http.MethodGet, path: fmt.Sprintf("/api/public/v1/sites/%d/annotations?from=2026-10-01&to=2026-10-31", f.foreignSite), want: http.StatusNotFound},
		{name: "missing credential", method: http.MethodGet, path: publicPath + "?from=2026-10-01&to=2026-10-31", want: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := f.request(t, tt.actor, tt.method, tt.path, tt.body)
			if response.Code != tt.want {
				t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), tt.want)
			}
		})
	}
}

func TestAnnotationsValidation(t *testing.T) {
	f := newAnnotationFixture(t)
	path := fmt.Sprintf("/api/private/sites/%d/annotations", f.siteID)
	tests := []struct {
		name, method, path, body string
	}{
		{name: "invalid day", method: http.MethodPost, path: path, body: `{"day":"2026-02-30","text":"Invalid"}`},
		{name: "blank text", method: http.MethodPost, path: path, body: `{"day":"2026-10-08","text":"   "}`},
		{name: "over 200 characters", method: http.MethodPost, path: path, body: fmt.Sprintf(`{"day":"2026-10-08","text":%q}`, strings.Repeat("é", 201))},
		{name: "missing range", method: http.MethodGet, path: path + "?from=2026-10-01"},
		{name: "reverse range", method: http.MethodGet, path: path + "?from=2026-10-09&to=2026-10-08"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := f.request(t, "editor", tt.method, tt.path, tt.body)
			if response.Code != http.StatusBadRequest || response.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("status=%d content-type=%q body=%s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
			}
		})
	}
}

func TestAnnotationsForeignIDIsHidden(t *testing.T) {
	f := newAnnotationFixture(t)
	var foreignID int64
	if err := f.pool.QueryRow(context.Background(), `insert into annotations(site_id,day,text) values ($1,'2026-10-08','Foreign') returning id`, f.foreignSite).Scan(&foreignID); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/private/annotations/%d", foreignID)
	for _, tt := range []struct{ method, body string }{
		{http.MethodPatch, `{"text":"Taken over"}`},
		{http.MethodDelete, ``},
	} {
		response := f.request(t, "editor", tt.method, path, tt.body)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s, want 404", tt.method, response.Code, response.Body.String())
		}
	}
	var text string
	if err := f.pool.QueryRow(context.Background(), `select text from annotations where id=$1`, foreignID).Scan(&text); err != nil || text != "Foreign" {
		t.Fatalf("foreign annotation changed: text=%q err=%v", text, err)
	}
}

func TestAnnotationsPublicWriteScopesAndAudit(t *testing.T) {
	f := newAnnotationFixture(t)
	for _, actor := range []string{"annotation-key", "sites-key"} {
		t.Run(actor, func(t *testing.T) {
			created := f.request(t, actor, http.MethodPost, fmt.Sprintf("/api/public/v1/sites/%d/annotations", f.siteID), `{"day":"2026-10-08","text":"Public note"}`)
			if created.Code != http.StatusCreated {
				t.Fatalf("POST status=%d body=%s", created.Code, created.Body.String())
			}
			var annotation annotationJSON
			if err := json.NewDecoder(created.Body).Decode(&annotation); err != nil {
				t.Fatal(err)
			}
			updated := f.request(t, actor, http.MethodPatch, fmt.Sprintf("/api/public/v1/annotations/%d", annotation.ID), `{"text":"Updated note"}`)
			if updated.Code != http.StatusOK {
				t.Fatalf("PATCH status=%d body=%s", updated.Code, updated.Body.String())
			}
			deleted := f.request(t, actor, http.MethodDelete, fmt.Sprintf("/api/public/v1/annotations/%d", annotation.ID), "")
			if deleted.Code != http.StatusNoContent {
				t.Fatalf("DELETE status=%d body=%s", deleted.Code, deleted.Body.String())
			}
		})
	}

	denied := f.request(t, "key", http.MethodPost, fmt.Sprintf("/api/public/v1/sites/%d/annotations", f.siteID), `{"day":"2026-10-08","text":"No"}`)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("stats key POST status=%d body=%s", denied.Code, denied.Body.String())
	}
	foreign := f.request(t, "annotation-key", http.MethodPost, fmt.Sprintf("/api/public/v1/sites/%d/annotations", f.foreignSite), `{"day":"2026-10-08","text":"No"}`)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign POST status=%d body=%s", foreign.Code, foreign.Body.String())
	}
	var audits int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from audit_log where action like 'annotation.%' and actor_key_id is not null`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 6 {
		t.Fatalf("API-key audit rows=%d, want 6", audits)
	}
}

type annotationJSON struct {
	ID int64 `json:"id"`
}
