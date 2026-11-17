package platformhttp_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
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

	for round, want := range []string{`"bodies_deleted":2,"events_anonymized":2`, `"bodies_deleted":0,"events_anonymized":0`} {
		res := f.request(t, "owner", http.MethodPost, path, body)
		if res.Code != http.StatusOK || !strings.Contains(strings.ReplaceAll(res.Body.String(), " ", ""), want) {
			t.Fatalf("round %d = %d %s", round, res.Code, res.Body)
		}
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
	if err := f.pool.QueryRow(ctx, `select count(*), coalesce(bool_or(metadata::text like '%alice%'),false) from audit_log where action='app.user_erased'`).Scan(&audits, &leaked); err != nil || audits != 2 || leaked {
		t.Fatalf("audits=%d leaked=%v err=%v", audits, leaked, err)
	}
}
