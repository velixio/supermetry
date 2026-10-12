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

	"supermetry/internal/db"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

type forgetRecorder struct{ slugs []string }

func (f *forgetRecorder) Forget(slugs ...string) { f.slugs = append(f.slugs, slugs...) }

type linkFixture struct {
	handler     http.Handler
	pool        *pgxpool.Pool
	siteID      int32
	otherSite   int32
	foreignSite int32
	userID      int64
	cache       *forgetRecorder
}

func newLinkFixture(t *testing.T) linkFixture {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, orgID, foreignOrgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('links@example.test','Linker','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Links','links-org') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Foreign','links-foreign') returning id`).Scan(&foreignOrgID); err != nil {
		t.Fatal(err)
	}
	var siteID, otherSite, foreignSite int32
	insertSite := func(org int64, publicID, zone string, out *int32) {
		if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,$2,'Site',$3,30) returning id`, org, publicID, zone).Scan(out); err != nil {
			t.Fatal(err)
		}
	}
	insertSite(orgID, "linksite0001", "Asia/Kathmandu", &siteID)
	insertSite(orgID, "linksite0002", "UTC", &otherSite)
	insertSite(foreignOrgID, "linksite0003", "UTC", &foreignSite)
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	cache := &forgetRecorder{}
	authenticator := actorAuthenticator{
		"owner":         {UserID: userID, OrgID: orgID, Role: authz.RoleOwner},
		"editor":        {UserID: userID, OrgID: orgID, Role: authz.RoleEditor},
		"viewer":        {UserID: userID, OrgID: orgID, Role: authz.RoleViewer},
		"other-editor":  {UserID: userID, OrgID: orgID, Role: authz.RoleEditor, SiteIDs: []int32{otherSite}},
		"foreign-owner": {UserID: userID, OrgID: foreignOrgID, Role: authz.RoleOwner},
		"key":           {KeyID: 11, OrgID: orgID, Role: authz.RoleKey, Scopes: []string{"stats:read", "sites:write"}},
		"share":         {ShareID: 5, OrgID: orgID, Role: authz.RoleShare, SiteIDs: []int32{siteID}},
	}
	return linkFixture{
		handler: platformhttp.New(platformhttp.Deps{
			Authenticator: authenticator, Sites: registry, Store: data, LinkCache: cache,
			BaseURL: "https://sm.example.test",
			Now:     func() time.Time { return time.Date(2026, 10, 12, 20, 30, 0, 0, time.UTC) },
		}),
		pool: pool, siteID: siteID, otherSite: otherSite, foreignSite: foreignSite, userID: userID, cache: cache,
	}
}

func (f linkFixture) request(t *testing.T, actor, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if actor != "" {
		request.Header.Set("X-Test-Actor", actor)
	}
	if method != http.MethodGet {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", "token")
		request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func (f linkFixture) create(t *testing.T, actor string, site int32, body string) (int64, map[string]any) {
	t.Helper()
	response := f.request(t, actor, http.MethodPost, fmt.Sprintf("/api/private/sites/%d/links", site), body)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	return int64(created["id"].(float64)), created
}

func TestLinksCRUDAuditAndCacheInvalidation(t *testing.T) {
	f := newLinkFixture(t)
	id, created := f.create(t, "editor", f.siteID, `{"slug":" Spring-Sale ","name":" Spring ","destination":"https://shop.example/offer?x=1","utm_source":"mail","utm_campaign":"spring","click_limit":100}`)
	if created["slug"] != "spring-sale" || created["name"] != "Spring" || created["status"] != "active" || created["enabled"] != true ||
		created["short_path"] != "/l/spring-sale" || created["short_url"] != "https://sm.example.test/l/spring-sale" ||
		created["utm_source"] != "mail" || created["utm_medium"] != "" || created["click_limit"] != float64(100) || created["clicks"] != float64(0) {
		t.Fatalf("created = %v", created)
	}
	autoID, auto := f.create(t, "owner", f.siteID, `{"name":"Auto","destination":"http://a.example/"}`)
	if slug, _ := auto["slug"].(string); len(slug) != 7 || autoID == id {
		t.Fatalf("generated slug = %v", auto["slug"])
	}

	path := fmt.Sprintf("/api/private/links/%d", id)
	got := f.request(t, "viewer", http.MethodGet, path, "")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"destination":"https://shop.example/offer?x=1"`) {
		t.Fatalf("viewer GET status=%d body=%s", got.Code, got.Body.String())
	}
	list := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/links", f.siteID), "")
	var listed []map[string]any
	if err := json.NewDecoder(list.Body).Decode(&listed); err != nil || len(listed) != 2 || listed[0]["id"].(float64) != float64(autoID) {
		t.Fatalf("list = %v, %v", listed, err)
	}

	future := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	patched := f.request(t, "editor", http.MethodPatch, path, `{"slug":"renamed","enabled":false,"expires_at":"`+future+`","utm_source":""}`)
	if patched.Code != http.StatusOK || !strings.Contains(patched.Body.String(), `"slug":"renamed"`) || !strings.Contains(patched.Body.String(), `"status":"disabled"`) || !strings.Contains(patched.Body.String(), `"utm_source":""`) {
		t.Fatalf("PATCH status=%d body=%s", patched.Code, patched.Body.String())
	}
	cleared := f.request(t, "editor", http.MethodPatch, path, `{"expires_at":null,"click_limit":null,"enabled":true}`)
	if cleared.Code != http.StatusOK || !strings.Contains(cleared.Body.String(), `"expires_at":null`) || !strings.Contains(cleared.Body.String(), `"click_limit":null`) || !strings.Contains(cleared.Body.String(), `"status":"active"`) {
		t.Fatalf("clearing PATCH status=%d body=%s", cleared.Code, cleared.Body.String())
	}
	if deleted := f.request(t, "editor", http.MethodDelete, path, ""); deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE status=%d", deleted.Code)
	}
	if gone := f.request(t, "owner", http.MethodGet, path, ""); gone.Code != http.StatusNotFound {
		t.Fatalf("GET after delete status=%d", gone.Code)
	}

	for _, slug := range []string{"spring-sale", "renamed"} {
		found := false
		for _, forgotten := range f.cache.slugs {
			found = found || forgotten == slug
		}
		if !found {
			t.Fatalf("cache was not told to forget %q: %v", slug, f.cache.slugs)
		}
	}

	rows, err := f.pool.Query(context.Background(), `select action, target_type, metadata::text from audit_log where action like 'link.%' order by id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action, target, metadata string
		if err := rows.Scan(&action, &target, &metadata); err != nil {
			t.Fatal(err)
		}
		if target != "link" || strings.Contains(metadata, "shop.example") || strings.Contains(metadata, "http") {
			t.Fatalf("audit row %s leaks or mislabels: %s %s", action, target, metadata)
		}
		actions = append(actions, action)
	}
	if strings.Join(actions, ",") != "link.created,link.created,link.updated,link.updated,link.deleted" {
		t.Fatalf("audit actions = %v", actions)
	}
}

func TestLinksAuthorizationMatrix(t *testing.T) {
	f := newLinkFixture(t)
	id, _ := f.create(t, "owner", f.siteID, `{"slug":"mine","name":"Mine","destination":"https://a.example"}`)
	foreignID, _ := f.create(t, "foreign-owner", f.foreignSite, `{"slug":"theirs","name":"Theirs","destination":"https://b.example"}`)
	otherID, _ := f.create(t, "owner", f.otherSite, `{"slug":"other","name":"Other","destination":"https://c.example"}`)

	site := fmt.Sprintf("/api/private/sites/%d", f.siteID)
	link := fmt.Sprintf("/api/private/links/%d", id)
	body := `{"name":"X","destination":"https://x.example"}`
	tests := []struct {
		name, actor, method, path, body string
		want                            int
	}{
		{"signed out list", "", http.MethodGet, site + "/links", "", http.StatusUnauthorized},
		{"signed out create", "", http.MethodPost, site + "/links", body, http.StatusUnauthorized},
		{"signed out get", "", http.MethodGet, link, "", http.StatusUnauthorized},
		{"signed out stats", "", http.MethodGet, link + "/stats", "", http.StatusUnauthorized},
		{"signed out patch", "", http.MethodPatch, link, `{"enabled":false}`, http.StatusUnauthorized},
		{"signed out delete", "", http.MethodDelete, link, "", http.StatusUnauthorized},
		{"signed out pixel", "", http.MethodGet, site + "/pixel", "", http.StatusUnauthorized},
		{"signed out pixel stats", "", http.MethodGet, site + "/pixel/stats", "", http.StatusUnauthorized},

		{"viewer lists", "viewer", http.MethodGet, site + "/links", "", http.StatusOK},
		{"viewer reads stats", "viewer", http.MethodGet, link + "/stats", "", http.StatusOK},
		{"viewer reads pixel", "viewer", http.MethodGet, site + "/pixel", "", http.StatusOK},
		{"viewer cannot create", "viewer", http.MethodPost, site + "/links", body, http.StatusForbidden},
		{"viewer cannot patch", "viewer", http.MethodPatch, link, `{"enabled":false}`, http.StatusForbidden},
		{"viewer cannot delete", "viewer", http.MethodDelete, link, "", http.StatusForbidden},

		{"editor creates", "editor", http.MethodPost, site + "/links", body, http.StatusCreated},
		{"editor patches", "editor", http.MethodPatch, link, `{"enabled":false}`, http.StatusOK},

		{"restricted editor: other site list hidden", "other-editor", http.MethodGet, site + "/links", "", http.StatusNotFound},
		{"restricted editor: other site create hidden", "other-editor", http.MethodPost, site + "/links", body, http.StatusNotFound},
		{"restricted editor: other site link hidden", "other-editor", http.MethodGet, link, "", http.StatusNotFound},
		{"restricted editor: other site patch hidden", "other-editor", http.MethodPatch, link, `{"enabled":false}`, http.StatusNotFound},
		{"restricted editor: other site delete hidden", "other-editor", http.MethodDelete, link, "", http.StatusNotFound},
		{"restricted editor: own site link", "other-editor", http.MethodGet, fmt.Sprintf("/api/private/links/%d", otherID), "", http.StatusOK},
		{"restricted editor: own site stats", "other-editor", http.MethodGet, fmt.Sprintf("/api/private/links/%d/stats", otherID), "", http.StatusOK},

		{"foreign org: site list hidden", "foreign-owner", http.MethodGet, site + "/links", "", http.StatusNotFound},
		{"foreign org: create hidden", "foreign-owner", http.MethodPost, site + "/links", body, http.StatusNotFound},
		{"foreign org: link get hidden", "foreign-owner", http.MethodGet, link, "", http.StatusNotFound},
		{"foreign org: stats hidden", "foreign-owner", http.MethodGet, link + "/stats", "", http.StatusNotFound},
		{"foreign org: patch hidden", "foreign-owner", http.MethodPatch, link, `{"enabled":false}`, http.StatusNotFound},
		{"foreign org: delete hidden", "foreign-owner", http.MethodDelete, link, "", http.StatusNotFound},
		{"foreign org: pixel hidden", "foreign-owner", http.MethodGet, site + "/pixel", "", http.StatusNotFound},
		{"owner cannot touch foreign link", "owner", http.MethodGet, fmt.Sprintf("/api/private/links/%d", foreignID), "", http.StatusNotFound},
		{"owner cannot delete foreign link", "owner", http.MethodDelete, fmt.Sprintf("/api/private/links/%d", foreignID), "", http.StatusNotFound},
		{"unknown link", "owner", http.MethodGet, "/api/private/links/999999", "", http.StatusNotFound},
		{"malformed link id", "owner", http.MethodGet, "/api/private/links/abc", "", http.StatusNotFound},

		{"api key cannot list", "key", http.MethodGet, site + "/links", "", http.StatusForbidden},
		{"api key cannot create", "key", http.MethodPost, site + "/links", body, http.StatusForbidden},
		{"api key cannot patch", "key", http.MethodPatch, link, `{"enabled":false}`, http.StatusForbidden},
		{"share cannot list", "share", http.MethodGet, site + "/links", "", http.StatusForbidden},
		{"share cannot read pixel stats", "share", http.MethodGet, site + "/pixel/stats", "", http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := f.request(t, test.actor, test.method, test.path, test.body)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), test.want)
			}
		})
	}
	// The forbidden and hidden attempts must not have changed anything.
	var enabled bool
	if err := f.pool.QueryRow(context.Background(), `select enabled from links where id=$1`, foreignID).Scan(&enabled); err != nil || !enabled {
		t.Fatalf("foreign link was modified: enabled=%v err=%v", enabled, err)
	}
	var count int
	_ = f.pool.QueryRow(context.Background(), `select count(*) from links where id in ($1,$2)`, foreignID, otherID).Scan(&count)
	if count != 2 {
		t.Fatalf("links were deleted by denied requests: %d", count)
	}
}

func TestLinksValidationRejectsUnsafeInput(t *testing.T) {
	f := newLinkFixture(t)
	path := fmt.Sprintf("/api/private/sites/%d/links", f.siteID)
	id, _ := f.create(t, "owner", f.siteID, `{"slug":"existing","name":"Existing","destination":"https://a.example"}`)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	bad := map[string]string{
		"javascript destination": `{"name":"x","destination":"javascript:alert(1)"}`,
		"data destination":       `{"name":"x","destination":"data:text/html;base64,PHNjcmlwdD4="}`,
		"file destination":       `{"name":"x","destination":"file:///etc/passwd"}`,
		"relative destination":   `{"name":"x","destination":"/admin"}`,
		"scheme relative":        `{"name":"x","destination":"//evil.example"}`,
		"credentials":            `{"name":"x","destination":"https://u:p@a.example"}`,
		"header injection":       "{\"name\":\"x\",\"destination\":\"https://a.example/\\r\\nSet-Cookie: a=b\"}",
		"oversized destination":  `{"name":"x","destination":"https://a.example/` + strings.Repeat("a", 2100) + `"}`,
		"missing destination":    `{"name":"x"}`,
		"points at short link":   `{"name":"x","destination":"https://sm.example.test/l/existing"}`,
		"reserved slug":          `{"slug":"admin","name":"x","destination":"https://a.example"}`,
		"slash in slug":          `{"slug":"a/b","name":"x","destination":"https://a.example"}`,
		"unicode slug":           `{"slug":"ünï","name":"x","destination":"https://a.example"}`,
		"short slug":             `{"slug":"a","name":"x","destination":"https://a.example"}`,
		"blank name":             `{"name":"  ","destination":"https://a.example"}`,
		"long utm":               `{"name":"x","destination":"https://a.example","utm_source":"` + strings.Repeat("a", 121) + `"}`,
		"zero click limit":       `{"name":"x","destination":"https://a.example","click_limit":0}`,
		"past expiry":            `{"name":"x","destination":"https://a.example","expires_at":"` + past + `"}`,
		"unknown field":          `{"name":"x","destination":"https://a.example","clicks":9999}`,
		"not json":               `nope`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			response := f.request(t, "owner", http.MethodPost, path, body)
			if response.Code != http.StatusBadRequest || response.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if dup := f.request(t, "owner", http.MethodPost, path, `{"slug":"EXISTING","name":"x","destination":"https://a.example"}`); dup.Code != http.StatusConflict {
		t.Fatalf("duplicate slug status=%d", dup.Code)
	}
	link := fmt.Sprintf("/api/private/links/%d", id)
	for name, body := range map[string]string{
		"patch javascript": `{"destination":"javascript:alert(1)"}`,
		"patch reserved":   `{"slug":"login"}`,
		"patch empty":      `{}`,
		"patch bad limit":  `{"click_limit":-4}`,
		"patch unknown":    `{"site_id":3}`,
	} {
		if response := f.request(t, "owner", http.MethodPatch, link, body); response.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d body=%s", name, response.Code, response.Body.String())
		}
	}
	var destination string
	_ = f.pool.QueryRow(context.Background(), `select destination from links where id=$1`, id).Scan(&destination)
	if destination != "https://a.example" {
		t.Fatalf("destination changed by rejected patches: %q", destination)
	}
	var count int
	_ = f.pool.QueryRow(context.Background(), `select count(*) from links`).Scan(&count)
	if count != 1 {
		t.Fatalf("rejected creates stored rows: %d", count)
	}
}

func TestLinkStatsReportSiteLocalDaysAndBreakdowns(t *testing.T) {
	f := newLinkFixture(t)
	id, _ := f.create(t, "owner", f.siteID, `{"slug":"stats","name":"Stats","destination":"https://a.example"}`)
	if _, err := f.pool.Exec(context.Background(), `insert into link_clicks_daily(link_id,day,country,referrer,device,clicks) values
		($1,'2026-10-10','NP','news.example','desktop',3),
		($1,'2026-10-11','NP','','mobile',4),
		($1,'2026-10-11','US','news.example','mobile',1),
		($1,'2026-09-01','NP','','desktop',9)`, id); err != nil {
		t.Fatal(err)
	}
	response := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/links/%d/stats?from=2026-10-01&to=2026-10-31", id), "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var stats struct {
		From, To string
		Total    int64
		Daily    []struct {
			Day   string
			Count int64
		}
		Countries []struct {
			Value  string
			Clicks int64
		}
		Referrers []struct {
			Value  string
			Clicks int64
		}
		Devices []struct {
			Value  string
			Clicks int64
		}
	}
	if err := json.NewDecoder(response.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	if stats.From != "2026-10-01" || stats.To != "2026-10-31" || stats.Total != 8 || len(stats.Daily) != 2 ||
		stats.Daily[0].Day != "2026-10-10" || stats.Daily[0].Count != 3 || stats.Daily[1].Count != 5 ||
		stats.Countries[0].Value != "NP" || stats.Countries[0].Clicks != 7 || stats.Countries[1].Value != "US" ||
		stats.Referrers[0].Value != "" || stats.Referrers[1].Value != "news.example" || stats.Referrers[0].Clicks != 4 || stats.Referrers[1].Clicks != 4 ||
		stats.Devices[0].Value != "mobile" || stats.Devices[0].Clicks != 5 {
		t.Fatalf("stats = %+v", stats)
	}
	for _, query := range []string{"from=2026-10-31&to=2026-10-01", "from=nope", "to=2026/10/01", "from=2020-01-01&to=2026-01-01"} {
		if r := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/links/%d/stats?%s", id, query), ""); r.Code != http.StatusBadRequest {
			t.Errorf("stats?%s status=%d", query, r.Code)
		}
	}
	defaults := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/links/%d/stats", id), "")
	// 20:30 UTC on the 12th is already the 13th in Asia/Kathmandu (+5:45): the default window ends there.
	if defaults.Code != http.StatusOK || !strings.Contains(defaults.Body.String(), `"from":"2026-09-14","to":"2026-10-13"`) {
		t.Fatalf("default range status=%d body=%s", defaults.Code, defaults.Body.String())
	}
}

func TestPixelSnippetAndStats(t *testing.T) {
	f := newLinkFixture(t)
	site := fmt.Sprintf("/api/private/sites/%d/pixel", f.siteID)
	ok := f.request(t, "viewer", http.MethodGet, site+"?campaign=spring-2026", "")
	if ok.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", ok.Code, ok.Body.String())
	}
	var snippet struct{ Campaign, Path, URL, HTML string }
	if err := json.NewDecoder(ok.Body).Decode(&snippet); err != nil {
		t.Fatal(err)
	}
	if snippet.Path != "/px/linksite0001.gif?c=spring-2026" || snippet.URL != "https://sm.example.test/px/linksite0001.gif?c=spring-2026" ||
		snippet.HTML != `<img src="https://sm.example.test/px/linksite0001.gif?c=spring-2026" width="1" height="1" alt="" style="display:none" />` {
		t.Fatalf("snippet = %+v", snippet)
	}
	plain := f.request(t, "viewer", http.MethodGet, site, "")
	if !strings.Contains(plain.Body.String(), `"url":"https://sm.example.test/px/linksite0001.gif"`) {
		t.Fatalf("plain snippet = %s", plain.Body.String())
	}
	for _, bad := range []string{`"><script>`, "a b", strings.Repeat("x", 65)} {
		if r := f.request(t, "viewer", http.MethodGet, site+"?campaign="+strings.ReplaceAll(bad, " ", "%20"), ""); r.Code != http.StatusBadRequest {
			t.Errorf("campaign %q status=%d", bad, r.Code)
		}
	}
	if _, err := f.pool.Exec(context.Background(), `insert into pixel_impressions_daily(site_id,day,campaign,country,impressions) values
		($1,'2026-10-10','spring','NP',5), ($1,'2026-10-10','spring','US',1), ($1,'2026-10-11','',  'NP',2), ($2,'2026-10-10','spring','NP',99)`, f.siteID, f.otherSite); err != nil {
		t.Fatal(err)
	}
	stats := f.request(t, "viewer", http.MethodGet, site+"/stats?from=2026-10-01&to=2026-10-31", "")
	if stats.Code != http.StatusOK || !strings.Contains(stats.Body.String(), `"total":8`) ||
		!strings.Contains(stats.Body.String(), `{"campaign":"spring","impressions":6}`) || strings.Contains(stats.Body.String(), "99") {
		t.Fatalf("stats status=%d body=%s", stats.Code, stats.Body.String())
	}
}
