package profileerasure_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/profileerasure"
	"supermetry/internal/testpg"
)

func TestEraseAppUserDeletesBodiesAndClearsHash(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	secret := []byte("0123456789abcdef0123456789abcdef")
	hashOf := func(id string) []byte {
		h := hmac.New(sha256.New, secret)
		h.Write([]byte(id))
		return h.Sum(nil)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(7,1,'appsite00001','App','UTC',30,'app'),(8,1,'appsite00002','Other','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app_hash_secrets(site_id,secret) values (7,$1)`, secret); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureAppEventPartitions(ctx, pool, mustTime("2026-11-01"), mustTime("2026-12-01")); err != nil {
		t.Fatal(err)
	}
	for _, e := range []struct {
		site int
		id   string
		user string
	}{{7, "a", "alice"}, {7, "b", "alice"}, {7, "c", "bob"}, {8, "d", "alice"}} {
		if _, err := pool.Exec(ctx, `insert into app_events(site_id,ts,received_at,event_id,kind,release,user_hash)
values ($1,'2026-11-17T10:00:00Z','2026-11-17T10:00:00Z',$2,'error','1.0',$3)`, e.site, e.id, hashOf(e.user)); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `insert into app_event_bodies(site_id,ts,event_id,body,size) values ($1,'2026-11-17T10:00:00Z',$2,'{}',2)`, e.site, e.id); err != nil {
			t.Fatal(err)
		}
	}
	bodies, events, err := profileerasure.EraseAppUser(ctx, pool, 7, "alice", 1) // batch 1 exercises the loops
	if err != nil {
		t.Fatal(err)
	}
	if bodies != 2 || events != 2 {
		t.Fatalf("bodies=%d events=%d, want 2 and 2", bodies, events)
	}
	var keptBodies, hashed, otherSite int
	if err := pool.QueryRow(ctx, `select (select count(*) from app_event_bodies where site_id=7),
 (select count(*) from app_events where site_id=7 and user_hash is not null),
 (select count(*) from app_event_bodies where site_id=8)`).Scan(&keptBodies, &hashed, &otherSite); err != nil {
		t.Fatal(err)
	}
	if keptBodies != 1 || hashed != 1 || otherSite != 1 {
		t.Fatalf("site 7 bodies=%d (want 1: bob), hashed events=%d (want 1: bob), site 8 bodies=%d (want 1)", keptBodies, hashed, otherSite)
	}
	var total int
	if err := pool.QueryRow(ctx, `select count(*) from app_events`).Scan(&total); err != nil || total != 4 {
		t.Fatalf("event rows must stay: %d %v", total, err)
	}
	if b, e, err := profileerasure.EraseAppUser(ctx, pool, 8, "alice", 10); err != nil || b != 0 || e != 0 {
		t.Fatalf("site without secret: %d %d %v", b, e, err)
	}
}

func mustTime(s string) time.Time {
	v, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return v
}
