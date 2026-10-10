package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/platform/lifecycle"
	"supermetry/internal/testpg"
)

func TestPendingDeletionTargetsSelectsEligibleRecipientsAndSites(t *testing.T) {
	pool := newNoticeStoreTest(t)
	ctx := context.Background()
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)

	orgID := insertNoticeOrg(t, pool, "Example Org", "example", nil)
	ownerID := insertNoticeUser(t, pool, "owner@example.test", false)
	adminID := insertNoticeUser(t, pool, "admin@example.test", false)
	editorID := insertNoticeUser(t, pool, "editor@example.test", false)
	deletedOwnerID := insertNoticeUser(t, pool, "deleted@example.test", true)
	insertNoticeMembership(t, pool, orgID, ownerID, "owner")
	insertNoticeMembership(t, pool, orgID, adminID, "admin")
	insertNoticeMembership(t, pool, orgID, editorID, "editor")
	insertNoticeMembership(t, pool, orgID, deletedOwnerID, "owner")

	deletedAt := now.Add(-24 * time.Hour)
	siteID := insertNoticeSite(t, pool, orgID, "site-notice1", "Standalone", deletedAt, false, false, nil)
	insertNoticeSite(t, pool, orgID, "site-demo001", "Demo", deletedAt, true, false, nil)
	insertNoticeSite(t, pool, orgID, "with-org0001", "With org", deletedAt, false, true, nil)
	insertNoticeSite(t, pool, orgID, "live-site001", "Live", time.Time{}, false, false, nil)

	orgDeletedAt := now.Add(-2 * time.Hour)
	deletedOrgID := insertNoticeOrg(t, pool, "Closing Org", "closing", &orgDeletedAt)
	closingOwnerID := insertNoticeUser(t, pool, "closing-owner@example.test", false)
	closingAdminID := insertNoticeUser(t, pool, "closing-admin@example.test", false)
	insertNoticeMembership(t, pool, deletedOrgID, closingOwnerID, "owner")
	insertNoticeMembership(t, pool, deletedOrgID, closingAdminID, "admin")
	insertNoticeSite(t, pool, deletedOrgID, "org-site0001", "Alpha", orgDeletedAt, false, true, nil)
	insertNoticeSite(t, pool, deletedOrgID, "org-site0002", "Beta", orgDeletedAt, false, true, nil)

	immediateDeletedAt := now.Add(-time.Hour)
	insertNoticeOrg(t, pool, "Empty Org", "empty", &immediateDeletedAt)

	targets, err := PendingDeletionTargets(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("PendingDeletionTargets() returned %d targets, want 2: %+v", len(targets), targets)
	}

	site := targets[0]
	if site.Kind != lifecycle.DeletionSite || site.ID != int64(siteID) || site.Name != "Standalone" || site.OrgSlug != "example" || !site.DeletedAt.Equal(deletedAt) {
		t.Fatalf("site target = %+v", site)
	}
	if got, want := fmt.Sprint(site.Recipients), "[owner@example.test admin@example.test]"; got != want {
		t.Fatalf("site recipients = %s, want %s", got, want)
	}

	org := targets[1]
	if org.Kind != lifecycle.DeletionOrg || org.ID != deletedOrgID || org.Name != "Closing Org" || org.OrgSlug != "closing" || !org.DeletedAt.Equal(orgDeletedAt) {
		t.Fatalf("org target = %+v", org)
	}
	if got, want := fmt.Sprint(org.Recipients), "[closing-owner@example.test]"; got != want {
		t.Fatalf("org recipients = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(org.Items), "[Alpha Beta]"; got != want {
		t.Fatalf("org sites = %s, want %s", got, want)
	}
}

func TestPendingDeletionTargetsMovesIndividuallyDeletedSiteIntoOrgNotice(t *testing.T) {
	pool := newNoticeStoreTest(t)
	ctx := context.Background()
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	orgDeletedAt := now.Add(-time.Hour)
	orgID := insertNoticeOrg(t, pool, "Closing Org", "closing", &orgDeletedAt)
	ownerID := insertNoticeUser(t, pool, "owner@example.test", false)
	insertNoticeMembership(t, pool, orgID, ownerID, "owner")
	insertNoticeSite(t, pool, orgID, "prior-site01", "Prior", now.Add(-48*time.Hour), false, false, nil)

	targets, err := PendingDeletionTargets(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Kind != lifecycle.DeletionOrg {
		t.Fatalf("targets = %+v, want one organization target", targets)
	}
	if got := fmt.Sprint(targets[0].Items); got != "[Prior]" {
		t.Fatalf("organization items = %s, want [Prior]", got)
	}
}

func TestMarkDeletionTargetUsesLifecycleTimestamp(t *testing.T) {
	pool := newNoticeStoreTest(t)
	ctx := context.Background()
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	orgID := insertNoticeOrg(t, pool, "Example Org", "example", nil)
	siteID := insertNoticeSite(t, pool, orgID, "mark-site001", "Mark", now.Add(-time.Hour), false, false, nil)
	target := lifecycle.DeletionTarget{Kind: lifecycle.DeletionSite, ID: int64(siteID), DeletedAt: now.Add(-time.Hour)}

	marked, err := MarkDeletionTarget(ctx, pool, target, lifecycle.DeletionNotified, now)
	if err != nil {
		t.Fatal(err)
	}
	if !marked {
		t.Fatal("MarkDeletionTarget() = false, want true")
	}

	stale := target
	stale.DeletedAt = stale.DeletedAt.Add(-time.Hour)
	marked, err = MarkDeletionTarget(ctx, pool, stale, lifecycle.DeletionReminded, now)
	if err != nil {
		t.Fatal(err)
	}
	if marked {
		t.Fatal("MarkDeletionTarget() marked a different deletion lifecycle")
	}

	var notifiedAt time.Time
	var remindedAt *time.Time
	if err := pool.QueryRow(ctx, `select deletion_notified_at,deletion_reminded_at from sites where id=$1`, siteID).Scan(&notifiedAt, &remindedAt); err != nil {
		t.Fatal(err)
	}
	if !notifiedAt.Equal(now) || remindedAt != nil {
		t.Fatalf("notice marks = (%s, %v), want (%s, nil)", notifiedAt, remindedAt, now)
	}
}

func TestRefreshDeletionTargetRejectsRestoredLifecycle(t *testing.T) {
	pool := newNoticeStoreTest(t)
	ctx := context.Background()
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	orgID := insertNoticeOrg(t, pool, "Example Org", "example", nil)
	userID := insertNoticeUser(t, pool, "owner@example.test", false)
	insertNoticeMembership(t, pool, orgID, userID, "owner")
	siteID := insertNoticeSite(t, pool, orgID, "fresh-site01", "Fresh", now.Add(-time.Hour), false, false, nil)
	target := lifecycle.DeletionTarget{Kind: lifecycle.DeletionSite, ID: int64(siteID), DeletedAt: now.Add(-time.Hour)}

	if _, err := pool.Exec(ctx, `update sites set deleted_at=null where id=$1`, siteID); err != nil {
		t.Fatal(err)
	}
	_, eligible, err := RefreshDeletionTarget(ctx, pool, target, now)
	if err != nil {
		t.Fatal(err)
	}
	if eligible {
		t.Fatal("RefreshDeletionTarget() kept a restored lifecycle eligible")
	}
}

func newNoticeStoreTest(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

func insertNoticeOrg(t *testing.T, pool *pgxpool.Pool, name, slug string, deletedAt *time.Time) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `insert into orgs(name,slug,deleted_at) values ($1,$2,$3) returning id`, name, slug, deletedAt).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertNoticeUser(t *testing.T, pool *pgxpool.Pool, email string, deleted bool) int64 {
	t.Helper()
	var deletedAt *time.Time
	if deleted {
		value := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
		deletedAt = &value
	}
	var id int64
	if err := pool.QueryRow(context.Background(), `insert into users(email,name,password_hash,deleted_at) values ($1,$1,'hash',$2) returning id`, email, deletedAt).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertNoticeMembership(t *testing.T, pool *pgxpool.Pool, orgID, userID int64, role string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `insert into memberships(org_id,user_id,role) values ($1,$2,$3)`, orgID, userID, role); err != nil {
		t.Fatal(err)
	}
}

func insertNoticeSite(t *testing.T, pool *pgxpool.Pool, orgID int64, publicID, name string, deletedAt time.Time, demo, withOrg bool, erasedAt *time.Time) int32 {
	t.Helper()
	var deleted any
	if !deletedAt.IsZero() {
		deleted = deletedAt
	}
	var id int32
	if err := pool.QueryRow(context.Background(), `
		insert into sites(org_id,public_id,name,timezone,retention_class,deleted_at,is_demo,deleted_with_org,erased_at)
		values ($1,$2,$3,'UTC',30,$4,$5,$6,$7) returning id`, orgID, publicID, name, deleted, demo, withOrg, erasedAt).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
