package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"supermetry/internal/enrich"
	"supermetry/internal/links"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
	coresites "supermetry/internal/sites"
)

func TestTrackingPixelRoutesAreRegistered(t *testing.T) {
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"owner": {UserID: 1, OrgID: 2, Role: authz.RoleOwner}},
		Sites:         siteRegistry{3: {ID: 3, OrgID: 2, PublicID: "routepixel01", TZ: time.UTC, Kind: coresites.KindWeb}},
	})
	request := httptest.NewRequest(http.MethodGet, "/api/private/sites/3/tracking-pixels", nil)
	request.Header.Set("X-Test-Actor", "owner")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s; route should reach its unavailable-store guard", response.Code, response.Body.String())
	}
}

func createTrackingPixel(t *testing.T, f linkFixture, actor string, siteID int32, body string) map[string]any {
	t.Helper()
	response := f.request(t, actor, http.MethodPost, fmt.Sprintf("/api/private/sites/%d/tracking-pixels", siteID), body)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	var pixel map[string]any
	if err := json.NewDecoder(response.Body).Decode(&pixel); err != nil {
		t.Fatal(err)
	}
	return pixel
}

func TestTrackingPixelsCRUDTotalsAndURLs(t *testing.T) {
	f := newLinkFixture(t)
	if _, err := f.pool.Exec(context.Background(), `insert into pixel_impressions_daily(site_id,day,campaign,country,impressions) values ($1,'2026-10-09','news.v1','CA',5)`, f.siteID); err != nil {
		t.Fatal(err)
	}
	created := createTrackingPixel(t, f, "editor", f.siteID, `{"name":" Newsletter ","tag":"news.v1"}`)
	id := int64(created["id"].(float64))
	if created["name"] != "Newsletter" || created["tag"] != "news.v1" ||
		created["gif_url"] != "https://sm.example.test/px/linksite0001.gif?c=news.v1" ||
		created["svg_url"] != "https://sm.example.test/px/linksite0001.svg?c=news.v1" ||
		created["html"] != `<img src="https://sm.example.test/px/linksite0001.gif?c=news.v1" width="1" height="1" alt="" style="display:none" />` ||
		created["total"] != float64(5) || created["views_30d"] != float64(5) {
		t.Fatalf("created = %v", created)
	}
	if _, _, ok := f.views.Snapshot(f.siteID, "news.v1", "2026-10-13", 1); !ok {
		t.Fatal("create did not register the badge key")
	}
	auto := createTrackingPixel(t, f, "owner", f.siteID, `{"name":"Automatic"}`)
	if tag, _ := auto["tag"].(string); !regexp.MustCompile(`^[a-z0-9]{8}$`).MatchString(tag) {
		t.Fatalf("generated tag = %q", tag)
	}

	if _, err := f.pool.Exec(context.Background(), `insert into pixel_impressions_daily(site_id,day,campaign,country,impressions) values
		($1,'2026-09-01','news.v1','US',11),($1,'2026-10-11','news.v1','US',4),($1,'2026-10-12','news.v1','NP',2)`, f.siteID); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/private/tracking-pixels/%d", id)
	got := f.request(t, "viewer", http.MethodGet, path, "")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"total":22`) || !strings.Contains(got.Body.String(), `"views_30d":11`) {
		t.Fatalf("get status=%d body=%s", got.Code, got.Body.String())
	}
	list := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/tracking-pixels", f.siteID), "")
	var listed []map[string]any
	if err := json.NewDecoder(list.Body).Decode(&listed); err != nil || len(listed) != 2 || listed[1]["tag"] != "news.v1" {
		t.Fatalf("list status=%d rows=%v err=%v", list.Code, listed, err)
	}

	patched := f.request(t, "editor", http.MethodPatch, path, `{"name":" Weekly news "}`)
	if patched.Code != http.StatusOK || !strings.Contains(patched.Body.String(), `"name":"Weekly news"`) || !strings.Contains(patched.Body.String(), `"tag":"news.v1"`) {
		t.Fatalf("patch status=%d body=%s", patched.Code, patched.Body.String())
	}
	if response := f.request(t, "editor", http.MethodPatch, path, `{"tag":"changed"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("mutable tag status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(t, "foreign-owner", http.MethodGet, path, ""); response.Code != http.StatusNotFound {
		t.Fatalf("foreign get status=%d body=%s", response.Code, response.Body.String())
	}

	if _, err := f.pool.Exec(context.Background(), `insert into pixel_impressions_daily(site_id,day,campaign,country,impressions) values ($1,'2026-10-12','keep','US',9)`, f.siteID); err != nil {
		t.Fatal(err)
	}
	if response := f.request(t, "editor", http.MethodDelete, path, ""); response.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	var removed, kept int
	if err := f.pool.QueryRow(context.Background(), `select count(*) filter (where campaign='news.v1'), count(*) filter (where campaign='keep') from pixel_impressions_daily where site_id=$1`, f.siteID).Scan(&removed, &kept); err != nil {
		t.Fatal(err)
	}
	if removed != 0 || kept != 1 {
		t.Fatalf("rows after delete: removed=%d kept=%d", removed, kept)
	}
	if _, _, ok := f.views.Snapshot(f.siteID, "news.v1", "2026-10-13", 1); ok {
		t.Fatal("delete retained the badge key")
	}
}

func TestTrackingPixelsValidationConflictsAndAuthorization(t *testing.T) {
	f := newLinkFixture(t)
	path := fmt.Sprintf("/api/private/sites/%d/tracking-pixels", f.siteID)
	existing := createTrackingPixel(t, f, "owner", f.siteID, `{"name":"Existing","tag":"same"}`)
	for name, test := range map[string]struct {
		body string
		want int
	}{
		"blank name":  {`{"name":"  ","tag":"blank"}`, http.StatusBadRequest},
		"invalid tag": {`{"name":"Bad","tag":"bad tag"}`, http.StatusBadRequest},
		"duplicate":   {`{"name":"Duplicate","tag":"same"}`, http.StatusConflict},
		"unknown":     {`{"name":"Bad","tag":"okay","enabled":true}`, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			response := f.request(t, "owner", http.MethodPost, path, test.body)
			if response.Code != test.want || response.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	pixelPath := fmt.Sprintf("/api/private/tracking-pixels/%d", int64(existing["id"].(float64)))
	for _, request := range []struct {
		method, target, body string
	}{
		{http.MethodPost, path, `{"name":"Denied","tag":"denied"}`},
		{http.MethodPatch, pixelPath, `{"name":"Denied"}`},
		{http.MethodDelete, pixelPath, ""},
	} {
		response := f.request(t, "viewer", request.method, request.target, request.body)
		if response.Code != http.StatusForbidden {
			t.Fatalf("viewer %s status=%d body=%s", request.method, response.Code, response.Body.String())
		}
	}
}

func TestTrackingPixelCreateEnablesCountingAndDeleteStopsIt(t *testing.T) {
	f := newLinkFixture(t)
	enricher, err := enrich.New("")
	if err != nil {
		t.Fatal(err)
	}
	recorder := links.NewRecorder(f.pool, links.RecorderOptions{})
	recorder.Views = f.views
	public := &links.Handler{
		Recorder: recorder,
		Sites:    f.registry,
		Enricher: enricher,
		Now:      func() time.Time { return f.now },
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /px/{file}", public.ServePixel)
	hit := func(target string) {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.RemoteAddr = "203.0.113.9:4000"
		request.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/124.0.0.0 Safari/537.36")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("pixel status=%d", response.Code)
		}
	}

	for _, target := range []string{
		"/px/linksite0001.gif",
		"/px/linksite0001.gif?c=%3Cinvalid%3E",
		"/px/linksite0001.gif?c=unregistered",
	} {
		hit(target)
	}
	if err := recorder.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var impressions int64
	if err := f.pool.QueryRow(context.Background(), `select count(*) from pixel_impressions_daily where site_id=$1`, f.siteID).Scan(&impressions); err != nil || impressions != 0 {
		t.Fatalf("rows after rejected hits = %d, err %v", impressions, err)
	}
	if _, _, ok := f.views.Snapshot(f.siteID, "unregistered", f.now.Format(time.DateOnly), 1); ok {
		t.Fatal("an unregistered hit changed the SiteViews cache")
	}

	created := createTrackingPixel(t, f, "editor", f.siteID, `{"name":"Lifecycle","tag":"lifecycle"}`)
	id := int64(created["id"].(float64))
	hit("/px/linksite0001.gif?c=lifecycle")
	if err := recorder.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `select coalesce(sum(impressions),0) from pixel_impressions_daily where site_id=$1 and campaign='lifecycle'`, f.siteID).Scan(&impressions); err != nil || impressions != 1 {
		t.Fatalf("impressions after create = %d, err %v", impressions, err)
	}

	response := f.request(t, "editor", http.MethodDelete, fmt.Sprintf("/api/private/tracking-pixels/%d", id), "")
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	hit("/px/linksite0001.gif?c=lifecycle")
	if err := recorder.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `select count(*) from pixel_impressions_daily where site_id=$1 and campaign='lifecycle'`, f.siteID).Scan(&impressions); err != nil || impressions != 0 {
		t.Fatalf("rows after delete and another hit = %d, err %v", impressions, err)
	}
}
