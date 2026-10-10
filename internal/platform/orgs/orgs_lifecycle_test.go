package orgs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/lifecycle"
)

type invalidationCounter struct {
	calls int
	err   error
}

func (i *invalidationCounter) Invalidate() error {
	i.calls++
	return i.err
}

func TestRestoreOrganization(t *testing.T) {
	service, ids, pool := newOrgWith(t, RoleOwner, RoleAdmin)
	ctx := context.Background()
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	deletedAt := now.Add(-3 * 24 * time.Hour)
	var restoredSiteID, individualSiteID, demoSiteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,deleted_at,deleted_with_org) values ($1,'org-restore1','With org','UTC',30,$2,true) returning id`, ids["org"], deletedAt).Scan(&restoredSiteID); err != nil {
		t.Fatal(err)
	}
	individualDeletedAt := deletedAt.Add(-3 * 24 * time.Hour)
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,deleted_at,deleted_with_org) values ($1,'org-restore2','Individual','UTC',30,$2,false) returning id`, ids["org"], individualDeletedAt).Scan(&individualSiteID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,is_demo,deleted_at,deleted_with_org) values ($1,'org-restore3','Demo','UTC',90,true,$2,true) returning id`, ids["org"], deletedAt).Scan(&demoSiteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update orgs set deleted_at=$2,deletion_notified_at=$2,deletion_reminded_at=$2 where id=$1`, ids["org"], deletedAt); err != nil {
		t.Fatal(err)
	}
	invalidator := &invalidationCounter{}
	service = New(service.store, invalidator)

	if err := service.Restore(ctx, ids[RoleAdmin], ids["org"], now); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin Restore() error = %v, want ErrForbidden", err)
	}
	if err := service.Restore(ctx, ids[RoleOwner], ids["org"], now); err != nil {
		t.Fatal(err)
	}
	if invalidator.calls != 1 {
		t.Fatalf("registry invalidations = %d, want 1", invalidator.calls)
	}
	var orgRestored, siteRestored bool
	if err := pool.QueryRow(ctx, `select deleted_at is null and deletion_notified_at is null and deletion_reminded_at is null from orgs where id=$1`, ids["org"]).Scan(&orgRestored); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select deleted_at is null and not deleted_with_org from sites where id=$1`, restoredSiteID).Scan(&siteRestored); err != nil {
		t.Fatal(err)
	}
	var keptDeletedAt time.Time
	if err := pool.QueryRow(ctx, `select deleted_at from sites where id=$1`, individualSiteID).Scan(&keptDeletedAt); err != nil {
		t.Fatal(err)
	}
	var demoStillDeleted, demoDetached bool
	if err := pool.QueryRow(ctx, `select deleted_at is not null,not deleted_with_org from sites where id=$1`, demoSiteID).Scan(&demoStillDeleted, &demoDetached); err != nil {
		t.Fatal(err)
	}
	if !orgRestored || !siteRestored || !keptDeletedAt.Equal(individualDeletedAt) || !demoStillDeleted || !demoDetached {
		t.Fatalf("restore state org=%t site=%t individual_deleted_at=%s demo_deleted=%t demo_detached=%t", orgRestored, siteRestored, keptDeletedAt, demoStillDeleted, demoDetached)
	}
	memberships, err := service.MembershipsFor(ctx, ids[RoleOwner])
	if err != nil || len(memberships) != 1 {
		t.Fatalf("MembershipsFor() = %+v, %v", memberships, err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `select count(*) from audit_log where action='organization.restored' and actor_user_id=$1 and org_id=$2`, ids[RoleOwner], ids["org"]).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("restore audit count = %d, err=%v", auditCount, err)
	}
}

func TestRestoreOrganizationRejectsTerminalStates(t *testing.T) {
	tests := []struct {
		name      string
		deletedAt func(time.Time) time.Time
		erased    bool
		scheduled bool
	}{
		{name: "grace elapsed", deletedAt: func(now time.Time) time.Time { return now.Add(-lifecycle.DeletionGrace) }, scheduled: true},
		{name: "already erased", deletedAt: func(now time.Time) time.Time { return now.Add(-time.Hour) }, erased: true, scheduled: true},
		{name: "immediate deletion", deletedAt: func(now time.Time) time.Time { return now.Add(-time.Hour) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, ids, pool := newOrgWith(t, RoleOwner)
			ctx := context.Background()
			now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
			deletedAt := test.deletedAt(now)
			var erasedAt *time.Time
			if test.erased {
				value := now.Add(-time.Minute)
				erasedAt = &value
			}
			if _, err := pool.Exec(ctx, `update orgs set deleted_at=$2,erased_at=$3 where id=$1`, ids["org"], deletedAt, erasedAt); err != nil {
				t.Fatal(err)
			}
			if test.scheduled {
				if _, err := pool.Exec(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,deleted_at,deleted_with_org) values ($1,'terminal0001','Terminal','UTC',30,$2,true)`, ids["org"], deletedAt); err != nil {
					t.Fatal(err)
				}
			}
			if err := service.Restore(ctx, ids[RoleOwner], ids["org"], now); !errors.Is(err, ErrNotRestorable) {
				t.Fatalf("Restore() error = %v, want ErrNotRestorable", err)
			}
		})
	}
}

func TestRestoreOrganizationSiteLimitAndPostCommitInvalidation(t *testing.T) {
	service, ids, pool := newOrgWith(t, RoleOwner)
	ctx := context.Background()
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	deletedAt := now.Add(-time.Hour)
	if _, err := pool.Exec(ctx, `update orgs set deleted_at=$2 where id=$1`, ids["org"], deletedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,deleted_at,deleted_with_org) values ($1,'restorelim01','Restore','UTC',30,$2,true),($1,'existinglive','Live','UTC',30,null,false)`, ids["org"], deletedAt); err != nil {
		t.Fatal(err)
	}
	service.SetSiteLimit(1)
	if err := service.Restore(ctx, ids[RoleOwner], ids["org"], now); !errors.Is(err, ErrSiteLimit) {
		t.Fatalf("Restore() error = %v, want ErrSiteLimit", err)
	}
	var stillDeleted bool
	if err := pool.QueryRow(ctx, `select deleted_at is not null from orgs where id=$1`, ids["org"]).Scan(&stillDeleted); err != nil || !stillDeleted {
		t.Fatalf("organization deleted=%t error=%v, want unchanged", stillDeleted, err)
	}

	service.SetSiteLimit(0)
	invalidator := &invalidationCounter{err: errors.New("refresh failed")}
	service.invalidator = invalidator
	if err := service.Restore(ctx, ids[RoleOwner], ids["org"], now); err != nil {
		t.Fatalf("Restore() after commit error = %v, want nil", err)
	}
}

func TestRestoreOrganizationScheduledByIndividuallyDeletedSite(t *testing.T) {
	service, ids, pool := newOrgWith(t, RoleOwner)
	ctx := context.Background()
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	orgDeletedAt := now.Add(-time.Hour)
	siteDeletedAt := now.Add(-48 * time.Hour)
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,deleted_at) values ($1,'priorreal001','Prior','UTC',30,$2) returning id`, ids["org"], siteDeletedAt).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update orgs set deleted_at=$2 where id=$1`, ids["org"], orgDeletedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into audit_log(org_id,actor_user_id,action,target_type,target_id,metadata) values ($1,$2,'organization.deleted','organization',$1,'{"scheduled":true}')`, ids["org"], ids[RoleOwner]); err != nil {
		t.Fatal(err)
	}

	if err := service.Restore(ctx, ids[RoleOwner], ids["org"], now); err != nil {
		t.Fatalf("Restore() error = %v, want nil", err)
	}
	var orgLive bool
	var keptSiteDeletedAt time.Time
	if err := pool.QueryRow(ctx, `select deleted_at is null from orgs where id=$1`, ids["org"]).Scan(&orgLive); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select deleted_at from sites where id=$1`, siteID).Scan(&keptSiteDeletedAt); err != nil {
		t.Fatal(err)
	}
	if !orgLive || !keptSiteDeletedAt.Equal(siteDeletedAt) {
		t.Fatalf("restored org live=%t prior site deleted_at=%s", orgLive, keptSiteDeletedAt)
	}
}

func TestRestoreOrganizationRejectsClaimedSite(t *testing.T) {
	service, ids, pool := newOrgWith(t, RoleOwner)
	ctx := context.Background()
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	deletedAt := now.Add(-time.Hour)
	if _, err := pool.Exec(ctx, `update orgs set deleted_at=$2 where id=$1`, ids["org"], deletedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,deleted_at,deleted_with_org,erasure_started_at) values ($1,'claimedorg01','Claimed','UTC',30,$2,true,$3)`, ids["org"], deletedAt, now); err != nil {
		t.Fatal(err)
	}

	if err := service.Restore(ctx, ids[RoleOwner], ids["org"], now); !errors.Is(err, ErrNotRestorable) {
		t.Fatalf("Restore() error = %v, want ErrNotRestorable", err)
	}
}

func TestRenameOrganization(t *testing.T) {
	service, ids, pool := newOrgWith(t, RoleOwner, RoleAdmin, RoleViewer)
	ctx := context.Background()
	tests := []struct {
		name      string
		principal authz.Principal
		value     string
		wantErr   error
	}{
		{name: "empty", principal: principal(ids[RoleOwner], RoleOwner, ids["org"]), value: "  ", wantErr: ErrInvalidName},
		{name: "too long", principal: principal(ids[RoleOwner], RoleOwner, ids["org"]), value: strings.Repeat("界", 101), wantErr: ErrInvalidName},
		{name: "control character", principal: principal(ids[RoleOwner], RoleOwner, ids["org"]), value: "Bad\nName", wantErr: ErrInvalidName},
		{name: "invalid UTF-8", principal: principal(ids[RoleOwner], RoleOwner, ids["org"]), value: string([]byte{0xff}), wantErr: ErrInvalidName},
		{name: "viewer forbidden", principal: principal(ids[RoleViewer], RoleViewer, ids["org"]), value: "Viewer Name", wantErr: ErrForbidden},
		{name: "admin allowed", principal: principal(ids[RoleAdmin], RoleAdmin, ids["org"]), value: "  Renamed Organization  "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := service.Rename(ctx, test.principal, test.value)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Rename() error = %v, want %v", err, test.wantErr)
			}
		})
	}
	var name string
	if err := pool.QueryRow(ctx, `select name from orgs where id=$1`, ids["org"]).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Renamed Organization" {
		t.Fatalf("organization name = %q", name)
	}
	var from, to string
	if err := pool.QueryRow(ctx, `select metadata->>'from',metadata->>'to' from audit_log where action='organization.renamed' and org_id=$1`, ids["org"]).Scan(&from, &to); err != nil {
		t.Fatal(err)
	}
	if from != "Test Organization" || to != "Renamed Organization" {
		t.Fatalf("rename audit = {%q, %q}", from, to)
	}
}
