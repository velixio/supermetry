package keys

import (
	"context"
	"errors"
	"testing"

	"panel/internal/platform/authz"
)

func TestSensitiveKeyScopesAndRevocation(t *testing.T) {
	service, owner, _ := newKeys(t)
	ctx := context.Background()
	plain, info, err := service.Create(ctx, owner, "sensitive", []string{"stats:read", "profiles:read", "replay:read", "data:export"}, []int32{10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := service.Verify(ctx, plain)
	if !ok {
		t.Fatal("new key did not verify")
	}
	for _, action := range []authz.Action{authz.ActionProfilesRead, authz.ActionReplayRead, authz.ActionDataExport} {
		if !authz.Can(p, action, 10) || authz.Can(p, action, 20) {
			t.Errorf("invalid scope for %s", action)
		}
	}
	if err := service.Revoke(ctx, owner, info.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.Verify(ctx, plain); ok {
		t.Fatal("revoked sensitive key verified")
	}
}

func TestKeyIssuanceRejectsInvalidAndStaleAuthority(t *testing.T) {
	service, owner, pool := newKeys(t)
	ctx := context.Background()
	for _, scopes := range [][]string{{"*"}, {"unknown"}, {"billing:manage"}} {
		if _, _, err := service.Create(ctx, owner, "invalid", scopes, nil, nil); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid scopes: %v", err)
		}
	}
	if _, _, err := service.Create(ctx, owner, "foreign", []string{"stats:read"}, []int32{999}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid sites: %v", err)
	}
	if _, err := pool.Exec(ctx, `update memberships set role='viewer',site_ids='{}',billing_admin=true where org_id=$1 and user_id=$2`, owner.OrgID, owner.UserID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Create(ctx, owner, "stale owner", []string{"profiles:read"}, nil, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale authority: %v", err)
	}
}

func TestRestrictedKeyManagerCannotRevokeOrListBroaderKey(t *testing.T) {
	service, owner, _ := newKeys(t)
	ctx := context.Background()
	plain, _, err := service.Create(ctx, owner, "restricted manager", []string{"keys:manage", "stats:read"}, []int32{10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager, ok := service.Verify(ctx, plain)
	if !ok {
		t.Fatal("manager key invalid")
	}
	_, broad, err := service.Create(ctx, owner, "all sites", []string{"stats:read"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, narrow, err := service.Create(ctx, manager, "own site", []string{"stats:read"}, []int32{10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	list, err := service.List(ctx, manager)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range list {
		if key.ID == broad.ID {
			t.Fatal("listed broader key")
		}
	}
	if err := service.Revoke(ctx, manager, broad.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked broader key: %v", err)
	}
	if err := service.Revoke(ctx, manager, narrow.ID); err != nil {
		t.Fatalf("could not revoke bounded key: %v", err)
	}
}

func TestHistoricalUnknownKeyScopesFailClosed(t *testing.T) {
	service, owner, pool := newKeys(t)
	ctx := context.Background()
	plain, info, err := service.Create(ctx, owner, "legacy", []string{"stats:read"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update api_keys set scopes=ARRAY['*']::text[] where id=$1`, info.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.Verify(ctx, plain); ok {
		t.Fatal("historical wildcard scope became authority")
	}
}
