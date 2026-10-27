package replay

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestRetentionHonorsOrgCap(t *testing.T) {
	ctx := context.Background()
	// Both sites store 60 days of retention in their own settings.
	pool, store, siteA, now := newRetentionFixture(t, 60, 1<<20)
	if _, err := pool.Exec(ctx, `create table replay_sessions_202608 partition of replay_sessions for values from ('2026-08-01') to ('2026-09-01')`); err != nil {
		t.Fatal(err)
	}
	var orgB int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Other','other-org') returning id`).Scan(&orgB); err != nil {
		t.Fatal(err)
	}
	var siteB int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id,public_id,name,timezone,retention_class,settings)
		values ($1,'replayothers','Other','UTC',30,
		        jsonb_build_object('replay', jsonb_build_object('enabled',true,'retention_days',60,'quota_bytes',1048576)))
		returning id`, orgB).Scan(&siteB); err != nil {
		t.Fatal(err)
	}
	// now is 2026-10-08, so a 45-day cap keeps days on or after 2026-08-24.
	// Ages 50, 40 and 20 days are 2026-08-19, 2026-08-29 and 2026-09-18.
	keys := map[int32][]string{}
	for _, site := range []struct {
		id   int32
		base int64
	}{{siteA, 0}, {siteB, 10}} {
		for i, age := range []int{50, 40, 20} {
			keys[site.id] = append(keys[site.id], addReplaySession(t, pool, store, site.id, site.base+int64(i)+1, now.AddDate(0, 0, -age), 10, nil))
		}
	}

	// Without a cap the 60-day site setting keeps everything: no behavior change.
	if err := RunRetention(ctx, pool, store, now); err != nil {
		t.Fatal(err)
	}
	assertReplaySessions(t, pool, siteA, []int64{1, 2, 3})
	assertReplaySessions(t, pool, siteB, []int64{11, 12, 13})

	// Lower organization A's cap to 45 days; B has none.
	if _, err := pool.Exec(ctx, `update orgs set replay_retention_cap_days=45 where id=(select org_id from sites where id=$1)`, siteA); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	if err := RunRetention(ctx, pool, store, now); err != nil {
		t.Fatal(err)
	}
	assertReplaySessions(t, pool, siteA, []int64{2, 3})
	assertRetainedBlobMissing(t, store, keys[siteA][0])
	assertBlobPresent(t, store, keys[siteA][1])
	assertBlobPresent(t, store, keys[siteA][2])
	assertReplaySessions(t, pool, siteB, []int64{11, 12, 13})
	for _, key := range keys[siteB] {
		assertBlobPresent(t, store, key)
	}
	wantLine := fmt.Sprintf("site_id=%d retention_days=45 org_cap_days=45 expired_sessions_deleted=1 sessions_kept=2", siteA)
	if !strings.Contains(logs.String(), wantLine) {
		t.Fatalf("log lacks %q:\n%s", wantLine, logs.String())
	}
	if got := strings.Count(logs.String(), fmt.Sprintf("site_id=%d ", siteA)); got != 1 {
		t.Fatalf("site A logged %d lines, want exactly 1", got)
	}

	// Raising the cap later does not bring anything back.
	if _, err := pool.Exec(ctx, `update orgs set replay_retention_cap_days=365`); err != nil {
		t.Fatal(err)
	}
	if err := RunRetention(ctx, pool, store, now); err != nil {
		t.Fatal(err)
	}
	assertReplaySessions(t, pool, siteA, []int64{2, 3})
	assertRetainedBlobMissing(t, store, keys[siteA][0])
	if _, err := pool.Exec(ctx, `update orgs set replay_retention_cap_days=null`); err != nil {
		t.Fatal(err)
	}
	if err := RunRetention(ctx, pool, store, now); err != nil {
		t.Fatal(err)
	}
	assertReplaySessions(t, pool, siteA, []int64{2, 3})
	assertReplaySessions(t, pool, siteB, []int64{11, 12, 13})
}
