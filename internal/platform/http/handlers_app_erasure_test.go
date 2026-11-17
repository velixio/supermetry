package platformhttp_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/profileerasure"
)

func TestEraseAppUser(t *testing.T) {
	f := newAppKeyFixture(t)
	ctx := context.Background()
	if err := db.EnsureAppEventPartitions(ctx, f.pool, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	secret := []byte("0123456789abcdef0123456789abcdef")
	if _, err := f.pool.Exec(ctx, `insert into app_hash_secrets(site_id,secret) values ($1,$2)`, f.siteID, secret); err != nil {
		t.Fatal(err)
	}
	hashOf := func(id string) []byte {
		h := hmac.New(sha256.New, secret)
		h.Write([]byte(id))
		return h.Sum(nil)
	}
	for _, e := range []struct{ id, user string }{{"a", "alice"}, {"b", "alice"}, {"c", "bob"}} {
		if _, err := f.pool.Exec(ctx, `insert into app_events(site_id,ts,received_at,event_id,kind,release,user_hash)
values ($1,'2026-10-12T10:00:00Z','2026-10-12T10:00:00Z',$2,'error','1.0',$3)`, f.siteID, e.id, hashOf(e.user)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(ctx, `insert into app_event_bodies(site_id,ts,event_id,body,size) values ($1,'2026-10-12T10:00:00Z',$2,'{}',2)`, f.siteID, e.id); err != nil {
			t.Fatal(err)
		}
	}
	path := fmt.Sprintf("/api/private/sites/%d/app/users/erase", f.siteID)
	body := `{"user_id":"alice"}`

	for _, actor := range []string{"viewer", "editor"} {
		if res := f.request(t, actor, http.MethodPost, path, body); res.Code != http.StatusForbidden {
			t.Fatalf("%s = %d", actor, res.Code)
		}
	}
	if res := f.request(t, "foreign-owner", http.MethodPost, path, body); res.Code != http.StatusNotFound {
		t.Fatalf("foreign owner = %d", res.Code)
	}
	for _, bad := range []string{`{}`, `{"user_id":" "}`, `{"user_id":"a","x":1}`, `nope`} {
		if res := f.request(t, "owner", http.MethodPost, path, bad); res.Code != http.StatusBadRequest {
			t.Fatalf("body %q = %d", bad, res.Code)
		}
	}
	var untouched int
	if err := f.pool.QueryRow(ctx, `select count(*) from app_event_bodies`).Scan(&untouched); err != nil || untouched != 3 {
		t.Fatalf("rejected calls changed data: bodies=%d err=%v", untouched, err)
	}

	res := f.request(t, "owner", http.MethodPost, path, body)
	if res.Code != http.StatusAccepted {
		t.Fatalf("erase = %d %s", res.Code, res.Body)
	}
	var accepted struct {
		ID        int64  `json:"id"`
		Status    string `json:"status"`
		StatusURL string `json:"status_url"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	wantURL := fmt.Sprintf("/api/private/sites/%d/app/users/erasures/%d", f.siteID, accepted.ID)
	if accepted.ID == 0 || accepted.Status != "pending" || accepted.StatusURL != wantURL || res.Header().Get("Location") != wantURL {
		t.Fatalf("accepted = %+v location=%q", accepted, res.Header().Get("Location"))
	}
	if again := f.request(t, "owner", http.MethodPost, path, body); again.Code != http.StatusAccepted || !strings.Contains(again.Body.String(), fmt.Sprintf(`"id":%d`, accepted.ID)) {
		t.Fatalf("repeat = %d %s, want the same request", again.Code, again.Body)
	}
	if err := f.pool.QueryRow(ctx, `select (select count(*) from app_event_bodies), (select count(*) from app_events where user_hash is not null)`).Scan(&untouched, new(int)); err != nil || untouched != 3 {
		t.Fatalf("request alone must not touch data: bodies=%d err=%v", untouched, err)
	}
	var stored string
	if err := f.pool.QueryRow(ctx, `select r::text from app_erasure_requests r where id=$1`, accepted.ID).Scan(&stored); err != nil || strings.Contains(stored, "alice") {
		t.Fatalf("stored row = %q err=%v", stored, err)
	}

	// Status: owner sees it, other roles and other orgs do not.
	if got := f.request(t, "owner", http.MethodGet, wantURL, ""); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"status":"pending"`) {
		t.Fatalf("status = %d %s", got.Code, got.Body)
	}
	if got := f.request(t, "viewer", http.MethodGet, wantURL, ""); got.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d", got.Code)
	}
	if got := f.request(t, "foreign-owner", http.MethodGet, wantURL, ""); got.Code != http.StatusNotFound {
		t.Fatalf("foreign status = %d", got.Code)
	}
	otherSitePath := fmt.Sprintf("/api/private/sites/%d/app/users/erasures/%d", f.otherSite, accepted.ID)
	if got := f.request(t, "owner", http.MethodGet, otherSitePath, ""); got.Code != http.StatusNotFound {
		t.Fatalf("request id under another site = %d", got.Code)
	}
	if got := f.request(t, "owner", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/app/users/erasures/abc", f.siteID), ""); got.Code != http.StatusNotFound {
		t.Fatalf("bad id = %d", got.Code)
	}

	// The worker finishes it: bodies gone, hash cleared, counts on the status, audit without the id.
	if err := profileerasure.NewAppWorker(f.pool, profileerasure.NewAppStore(f.pool)).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got := f.request(t, "owner", http.MethodGet, wantURL, "")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"bodies_deleted":2`) {
		t.Fatalf("done status = %d %s", got.Code, got.Body)
	}
	if !strings.Contains(got.Body.String(), `"events_anonymized":2`) || !strings.Contains(got.Body.String(), `"status":"done"`) {
		t.Fatalf("done status = %s", got.Body)
	}
	var bodies, hashed int
	if err := f.pool.QueryRow(ctx, `select (select count(*) from app_event_bodies), (select count(*) from app_events where user_hash is not null)`).Scan(&bodies, &hashed); err != nil {
		t.Fatal(err)
	}
	if bodies != 1 || hashed != 1 {
		t.Fatalf("bodies=%d hashed=%d, want only bob's left", bodies, hashed)
	}
	var audits int
	var leaked bool
	if err := f.pool.QueryRow(ctx, `select count(*), coalesce(bool_or(metadata::text like '%alice%'),false) from audit_log where action='app.user_erased'`).Scan(&audits, &leaked); err != nil || audits != 1 || leaked {
		t.Fatalf("audits=%d leaked=%v err=%v", audits, leaked, err)
	}
}
