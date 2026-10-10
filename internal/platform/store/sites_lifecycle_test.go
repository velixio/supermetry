package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/platform/lifecycle"
	"supermetry/internal/testpg"
)

func TestSitesDueForErasure(t *testing.T) {
	pool, orgID := newSiteLifecycleStore(t)
	ctx := context.Background()
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	demo := insertLifecycleSite(t, pool, orgID, "due-demo0001", now, true, nil)
	old := insertLifecycleSite(t, pool, orgID, "due-real0001", now.Add(-lifecycle.DeletionGrace), false, nil)
	insertLifecycleSite(t, pool, orgID, "keep-real001", now.Add(-lifecycle.DeletionGrace+time.Second), false, nil)
	erasedAt := now.Add(-time.Hour)
	insertLifecycleSite(t, pool, orgID, "done-real001", now.Add(-lifecycle.DeletionGrace-time.Hour), false, &erasedAt)
	if _, err := pool.Exec(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,is_demo) values ($1,'live-demo001','Live','UTC',30,true)`, orgID); err != nil {
		t.Fatal(err)
	}

	got, err := SitesDueForErasure(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []int32{demo, old}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("SitesDueForErasure() = %v, want %v", got, want)
	}
}

func TestMarkSiteErased(t *testing.T) {
	pool, orgID := newSiteLifecycleStore(t)
	ctx := context.Background()
	deletedAt := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	siteID := insertLifecycleSite(t, pool, orgID, "mark-real001", deletedAt, false, nil)
	erasedAt := deletedAt.Add(lifecycle.DeletionGrace)

	if err := MarkSiteErased(ctx, pool, siteID, erasedAt); err != nil {
		t.Fatal(err)
	}
	var column time.Time
	var setting string
	if err := pool.QueryRow(ctx, `select erased_at,settings->>'erased_at' from sites where id=$1`, siteID).Scan(&column, &setting); err != nil {
		t.Fatal(err)
	}
	if !column.Equal(erasedAt) || setting != erasedAt.UTC().Format(time.RFC3339) {
		t.Fatalf("erasure marks = (%s, %q), want (%s, %q)", column, setting, erasedAt, erasedAt.UTC().Format(time.RFC3339))
	}
}

func TestDeletedSites(t *testing.T) {
	pool, orgID := newSiteLifecycleStore(t)
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	deletedAt := now.Add(-24 * time.Hour)
	siteID := insertLifecycleSite(t, pool, orgID, "list-real001", deletedAt, false, nil)
	if _, err := pool.Exec(context.Background(), `update sites set deleted_with_org=true where id=$1`, siteID); err != nil {
		t.Fatal(err)
	}
	insertLifecycleSite(t, pool, orgID, "list-demo001", deletedAt, true, nil)
	insertLifecycleSite(t, pool, orgID, "list-old0001", now.Add(-lifecycle.DeletionGrace), false, nil)

	got, err := DeletedSites(context.Background(), pool, orgID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("DeletedSites() returned %d sites, want 1: %+v", len(got), got)
	}
	if got[0].ID != siteID || got[0].PublicID != "list-real001" || got[0].Name != "list-real001" || !got[0].DeletedAt.Equal(deletedAt) || !got[0].PurgeAt.Equal(deletedAt.Add(lifecycle.DeletionGrace)) || !got[0].WithOrg {
		t.Fatalf("DeletedSites()[0] = %+v", got[0])
	}
}

func TestRestoreSiteRules(t *testing.T) {
	tests := []struct {
		name       string
		deletedAgo time.Duration
		demo       bool
		erased     bool
		deletedOrg bool
		wantErr    bool
	}{
		{name: "inside grace", deletedAgo: lifecycle.DeletionGrace - time.Second},
		{name: "exactly at grace boundary", deletedAgo: lifecycle.DeletionGrace, wantErr: true},
		{name: "after erase", deletedAgo: time.Hour, erased: true, wantErr: true},
		{name: "demo", deletedAgo: time.Hour, demo: true, wantErr: true},
		{name: "deleted organization", deletedAgo: time.Hour, deletedOrg: true, wantErr: true},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, orgID := newSiteLifecycleStore(t)
			ctx := context.Background()
			now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
			var erasedAt *time.Time
			if test.erased {
				value := now.Add(-time.Minute)
				erasedAt = &value
			}
			siteID := insertLifecycleSite(t, pool, orgID, fmt.Sprintf("rule-site%03d", index), now.Add(-test.deletedAgo), test.demo, erasedAt)
			if _, err := pool.Exec(ctx, `update sites set deleted_with_org=true,deletion_notified_at=$2,deletion_reminded_at=$2 where id=$1`, siteID, now.Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			if test.deletedOrg {
				if _, err := pool.Exec(ctx, `update orgs set deleted_at=$2 where id=$1`, orgID, now.Add(-time.Hour)); err != nil {
					t.Fatal(err)
				}
			}

			err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
				return RestoreSite(ctx, tx, orgID, siteID, now)
			})
			if test.wantErr {
				if !errors.Is(err, ErrNotRestorable) {
					t.Fatalf("RestoreSite() error = %v, want ErrNotRestorable", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var restored bool
			if err := pool.QueryRow(ctx, `select deleted_at is null and not deleted_with_org and deletion_notified_at is null and deletion_reminded_at is null from sites where id=$1`, siteID).Scan(&restored); err != nil {
				t.Fatal(err)
			}
			if !restored {
				t.Fatal("restore did not clear lifecycle state")
			}
		})
	}
}

func TestRestoreAndErasureClaimAreMutuallyExclusive(t *testing.T) {
	t.Run("claimed site cannot restore", func(t *testing.T) {
		pool, orgID := newSiteLifecycleStore(t)
		ctx := context.Background()
		now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
		siteID := insertLifecycleSite(t, pool, orgID, "claim-site01", now.Add(-lifecycle.DeletionGrace), false, nil)
		claimed, err := ClaimSiteErasure(ctx, pool, siteID, now)
		if err != nil || !claimed {
			t.Fatalf("ClaimSiteErasure() = (%t, %v), want (true, nil)", claimed, err)
		}
		err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return RestoreSite(ctx, tx, orgID, siteID, now.Add(-time.Second)) })
		if !errors.Is(err, ErrNotRestorable) {
			t.Fatalf("RestoreSite() error = %v, want ErrNotRestorable", err)
		}
	})

	t.Run("restored site cannot be claimed", func(t *testing.T) {
		pool, orgID := newSiteLifecycleStore(t)
		ctx := context.Background()
		now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
		siteID := insertLifecycleSite(t, pool, orgID, "restorefst01", now.Add(-time.Hour), false, nil)
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return RestoreSite(ctx, tx, orgID, siteID, now) }); err != nil {
			t.Fatal(err)
		}
		claimed, err := ClaimSiteErasure(ctx, pool, siteID, now.Add(lifecycle.DeletionGrace))
		if err != nil || claimed {
			t.Fatalf("ClaimSiteErasure() = (%t, %v), want (false, nil)", claimed, err)
		}
	})
}

func TestDeleteRestoreDeleteStartsNewLifecycle(t *testing.T) {
	pool, orgID := newSiteLifecycleStore(t)
	ctx := context.Background()
	firstDeletedAt := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	siteID := insertLifecycleSite(t, pool, orgID, "again-real01", firstDeletedAt, false, nil)
	if _, err := pool.Exec(ctx, `update sites set deletion_notified_at=$2,deletion_reminded_at=$2 where id=$1`, siteID, firstDeletedAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return RestoreSite(ctx, tx, orgID, siteID, firstDeletedAt.Add(24*time.Hour))
	}); err != nil {
		t.Fatal(err)
	}
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return SoftDeleteSite(ctx, tx, orgID, siteID)
	}); err != nil {
		t.Fatal(err)
	}

	var secondDeletedAt time.Time
	var noticesClear bool
	if err := pool.QueryRow(ctx, `select deleted_at,deletion_notified_at is null and deletion_reminded_at is null from sites where id=$1`, siteID).Scan(&secondDeletedAt, &noticesClear); err != nil {
		t.Fatal(err)
	}
	if !secondDeletedAt.After(firstDeletedAt) || !noticesClear {
		t.Fatalf("second lifecycle deleted_at=%s notices_clear=%t", secondDeletedAt, noticesClear)
	}
}

func newSiteLifecycleStore(t *testing.T) (*pgxpool.Pool, int64) {
	t.Helper()
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(context.Background(), `insert into orgs(name,slug) values ('Lifecycle','lifecycle') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	return pool, orgID
}

func insertLifecycleSite(t *testing.T, pool *pgxpool.Pool, orgID int64, publicID string, deletedAt time.Time, demo bool, erasedAt *time.Time) int32 {
	t.Helper()
	var siteID int32
	if err := pool.QueryRow(context.Background(), `
		insert into sites(org_id,public_id,name,timezone,retention_class,is_demo,deleted_at,erased_at)
		values ($1,$2,$2,'UTC',30,$3,$4,$5) returning id`, orgID, publicID, demo, deletedAt, erasedAt).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	return siteID
}
