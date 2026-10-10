package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/platform/lifecycle"
	"supermetry/internal/testpg"
)

func TestDeletedOrgsForOwner(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	var userID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('deleted-owner@example.test','Owner','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	sequence := 0
	insertOrg := func(name, slug string, deletedAt time.Time, erased bool, scheduled bool) int64 {
		t.Helper()
		sequence++
		var orgID int64
		var erasedAt *time.Time
		if erased {
			value := now.Add(-time.Minute)
			erasedAt = &value
		}
		if err := pool.QueryRow(ctx, `insert into orgs(name,slug,deleted_at,erased_at) values ($1,$2,$3,$4) returning id`, name, slug, deletedAt, erasedAt).Scan(&orgID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, orgID, userID); err != nil {
			t.Fatal(err)
		}
		if scheduled {
			if _, err := pool.Exec(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,deleted_at,deleted_with_org) values ($1,$2,$3,'UTC',30,$4,true)`, orgID, fmt.Sprintf("%012d", sequence), name+" Site", deletedAt); err != nil {
				t.Fatal(err)
			}
		}
		return orgID
	}
	wantID := insertOrg("Scheduled", "scheduled", now.Add(-24*time.Hour), false, true)
	priorID := insertOrg("Prior", "prior", now.Add(-time.Hour), false, false)
	if _, err := pool.Exec(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,deleted_at) values ($1,'prioronly001','Prior Site','UTC',30,$2)`, priorID, now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into audit_log(org_id,actor_user_id,action,target_type,target_id,metadata) values ($1,$2,'organization.deleted','organization',$1,'{"scheduled":true}')`, priorID, userID); err != nil {
		t.Fatal(err)
	}
	insertOrg("Immediate", "immediate", now.Add(-24*time.Hour), false, false)
	insertOrg("Expired", "expired", now.Add(-lifecycle.DeletionGrace), false, true)
	insertOrg("Erased", "erased", now.Add(-24*time.Hour), true, true)

	got, err := DeletedOrgsForOwner(ctx, pool, userID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("DeletedOrgsForOwner() returned %d orgs, want 2: %+v", len(got), got)
	}
	if got[0].ID != priorID || len(got[0].Sites) != 1 || got[0].Sites[0] != "Prior Site" {
		t.Fatalf("DeletedOrgsForOwner()[0] = %+v, want prior-only scheduled organization", got[0])
	}
	wantDeletedAt := now.Add(-24 * time.Hour)
	if got[1].ID != wantID || got[1].Name != "Scheduled" || got[1].Slug != "scheduled" || !got[1].DeletedAt.Equal(wantDeletedAt) || !got[1].PurgeAt.Equal(wantDeletedAt.Add(lifecycle.DeletionGrace)) {
		t.Fatalf("DeletedOrgsForOwner()[1] = %+v", got[1])
	}
	if len(got[1].Sites) != 1 || got[1].Sites[0] != "Scheduled Site" {
		t.Fatalf("DeletedOrgsForOwner()[1].Sites = %v", got[1].Sites)
	}
}
