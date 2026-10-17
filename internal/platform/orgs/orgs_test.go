package orgs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

func TestNoEscalationAndLastOwner(t *testing.T) {
	service, ids, _ := newOrgWith(t, RoleOwner, RoleAdmin)
	ctx := context.Background()
	admin := principal(ids[RoleAdmin], RoleAdmin, ids["org"])

	if err := service.SetRole(ctx, admin, ids[RoleAdmin], RoleOwner, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin self-promotion error = %v, want ErrForbidden", err)
	}
	if err := service.SetRole(ctx, admin, ids[RoleOwner], RoleViewer, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin owner-demotion error = %v, want ErrForbidden", err)
	}
	owner := principal(ids[RoleOwner], RoleOwner, ids["org"])
	if err := service.SetRole(ctx, owner, ids[RoleOwner], RoleAdmin, nil); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("last-owner demotion error = %v, want ErrLastOwner", err)
	}
}

func TestSetRoleAndRemoveAreAudited(t *testing.T) {
	service, ids, pool := newOrgWith(t, RoleOwner, RoleAdmin, RoleViewer)
	ctx := context.Background()
	owner := principal(ids[RoleOwner], RoleOwner, ids["org"])

	sites := []int32{accessTestSite(t, pool, ids["org"], "orgtestsite1"), accessTestSite(t, pool, ids["org"], "orgtestsite2")}
	if err := service.SetRole(ctx, owner, ids[RoleViewer], RoleEditor, sites); err != nil {
		t.Fatal("set role:", err)
	}
	if err := service.Remove(ctx, owner, ids[RoleAdmin]); err != nil {
		t.Fatal("remove member:", err)
	}

	memberships, err := service.MembershipsFor(ctx, ids[RoleViewer])
	if err != nil {
		t.Fatal("list memberships:", err)
	}
	if len(memberships) != 1 || memberships[0].Role != RoleEditor || !equalSites(memberships[0].SiteIDs, sites) {
		t.Fatalf("memberships = %+v", memberships)
	}
	var removed int
	if err := pool.QueryRow(ctx, `select count(*) from memberships where org_id = $1 and user_id = $2`, ids["org"], ids[RoleAdmin]).Scan(&removed); err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("removed membership count = %d, want 0", removed)
	}

	rows, err := pool.Query(ctx, `select action, target_id from audit_log where org_id = $1 order by id`, ids["org"])
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var action string
		var targetID int64
		if err := rows.Scan(&action, &targetID); err != nil {
			t.Fatal(err)
		}
		got = append(got, action)
		if targetID == 0 {
			t.Fatal("audit target ID is zero")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "membership.role_changed" || got[1] != "membership.removed" {
		t.Fatalf("audit actions = %v", got)
	}
}

func TestRemoveProtectsOwners(t *testing.T) {
	service, ids, _ := newOrgWith(t, RoleOwner, RoleAdmin)
	ctx := context.Background()
	admin := principal(ids[RoleAdmin], RoleAdmin, ids["org"])
	if err := service.Remove(ctx, admin, ids[RoleOwner]); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin removing owner error = %v, want ErrForbidden", err)
	}
	owner := principal(ids[RoleOwner], RoleOwner, ids["org"])
	if err := service.Remove(ctx, owner, ids[RoleOwner]); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("last owner removal error = %v, want ErrLastOwner", err)
	}
}

func TestRemovedActorCannotUseStalePrincipal(t *testing.T) {
	service, ids, _ := newOrgWith(t, RoleOwner, RoleAdmin, RoleViewer)
	ctx := context.Background()
	owner := principal(ids[RoleOwner], RoleOwner, ids["org"])
	staleAdmin := principal(ids[RoleAdmin], RoleAdmin, ids["org"])

	if err := service.Remove(ctx, owner, ids[RoleAdmin]); err != nil {
		t.Fatal("remove admin:", err)
	}
	if err := service.SetRole(ctx, staleAdmin, ids[RoleViewer], RoleEditor, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale admin role change error = %v, want ErrForbidden", err)
	}
}

func TestDemotedActorCannotUseStalePrincipal(t *testing.T) {
	service, ids, _ := newOrgWith(t, RoleOwner, RoleAdmin, RoleViewer)
	ctx := context.Background()
	owner := principal(ids[RoleOwner], RoleOwner, ids["org"])

	if err := service.SetRole(ctx, owner, ids[RoleAdmin], RoleOwner, nil); err != nil {
		t.Fatal("promote second owner:", err)
	}
	staleOwner := principal(ids[RoleAdmin], RoleOwner, ids["org"])
	if err := service.SetRole(ctx, owner, ids[RoleAdmin], RoleViewer, nil); err != nil {
		t.Fatal("demote second owner:", err)
	}
	if err := service.SetRole(ctx, staleOwner, ids[RoleAdmin], RoleOwner, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale owner role change error = %v, want ErrForbidden", err)
	}
}

func TestAuditFailureRollsBackMembershipMutation(t *testing.T) {
	service, ids, pool := newOrgWith(t, RoleOwner, RoleViewer)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		create function reject_audit() returns trigger language plpgsql as $$
		begin
			raise exception 'audit unavailable';
		end
		$$;
		create trigger reject_audit before insert on audit_log
		for each row execute function reject_audit()`); err != nil {
		t.Fatal("install audit failure trigger:", err)
	}

	owner := principal(ids[RoleOwner], RoleOwner, ids["org"])
	if err := service.SetRole(ctx, owner, ids[RoleViewer], RoleEditor, nil); err == nil {
		t.Fatal("set role succeeded despite audit failure")
	}
	var role Role
	if err := pool.QueryRow(ctx, `select role from memberships where org_id = $1 and user_id = $2`, ids["org"], ids[RoleViewer]).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != RoleViewer {
		t.Fatalf("role after failed audit = %q, want %q", role, RoleViewer)
	}
}

func TestAuditWrittenInSameTx(t *testing.T) {
	_, ids, pool := newOrgWith(t, RoleOwner)
	ctx := context.Background()
	actor := principal(ids[RoleOwner], RoleOwner, ids["org"])

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.Write(ctx, tx, actor, "org.changed", "org", ids["org"], map[string]any{"name": "rolled back"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertAuditCount(t, pool, ids["org"], 0)

	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.Write(ctx, tx, actor, "org.changed", "org", ids["org"], map[string]any{"name": "committed"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	assertAuditCount(t, pool, ids["org"], 1)

	var actorID int64
	var metadata []byte
	if err := pool.QueryRow(ctx, `select actor_user_id, metadata from audit_log where org_id = $1`, ids["org"]).Scan(&actorID, &metadata); err != nil {
		t.Fatal(err)
	}
	if actorID != ids[RoleOwner] {
		t.Fatalf("actor_user_id = %d, want %d", actorID, ids[RoleOwner])
	}
	var decoded map[string]any
	if err := json.Unmarshal(metadata, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["name"] != "committed" {
		t.Fatalf("metadata = %v", decoded)
	}
}

func newOrgWith(t *testing.T, roles ...Role) (*Orgs, map[Role]int64, *pgxpool.Pool) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	service := New(&store.Store{Pool: pool})
	ids := make(map[Role]int64, len(roles)+1)
	for i, role := range roles {
		var userID int64
		if err := pool.QueryRow(ctx, `insert into users(email, name, password_hash) values ($1, $2, 'hash') returning id`, string(role)+string(rune('a'+i))+"@example.test", string(role)).Scan(&userID); err != nil {
			t.Fatal(err)
		}
		ids[role] = userID
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	orgID, err := service.Create(ctx, tx, "Test Organization", ids[RoleOwner])
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	ids["org"] = orgID
	for _, role := range roles {
		if role == RoleOwner {
			continue
		}
		if _, err := pool.Exec(ctx, `insert into memberships(org_id, user_id, role) values ($1, $2, $3)`, orgID, ids[role], role); err != nil {
			t.Fatal(err)
		}
	}
	return service, ids, pool
}

func principal(userID int64, role Role, orgID int64) authz.Principal {
	return authz.Principal{UserID: userID, OrgID: orgID, Role: authz.Role(role)}
}

func assertAuditCount(t *testing.T, pool *pgxpool.Pool, orgID int64, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(context.Background(), `select count(*) from audit_log where org_id = $1`, orgID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("audit count = %d, want %d", got, want)
	}
}

func equalSites(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLeaveRemovesOnlyTheActorAndProtectsTheLastOwner(t *testing.T) {
	service, ids, pool := newOrgWith(t, RoleOwner, RoleViewer)
	ctx := context.Background()
	viewer := principal(ids[RoleViewer], RoleViewer, ids["org"])
	if err := service.Leave(ctx, viewer); err != nil {
		t.Fatal("viewer leave:", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from memberships where org_id=$1`, ids["org"]).Scan(&count); err != nil || count != 1 {
		t.Fatalf("memberships after leave = %d, %v", count, err)
	}
	var audited int
	if err := pool.QueryRow(ctx, `select count(*) from audit_log where action='membership.left' and target_id=$1`, ids[RoleViewer]).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("membership.left audit rows = %d, %v", audited, err)
	}
	if err := service.Leave(ctx, viewer); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second leave err = %v, want ErrNotFound", err)
	}
	owner := principal(ids[RoleOwner], RoleOwner, ids["org"])
	if err := service.Leave(ctx, owner); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("last owner leave err = %v, want ErrLastOwner", err)
	}
}
