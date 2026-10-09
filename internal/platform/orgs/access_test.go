package orgs

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"panel/internal/platform/authz"
)

func TestAccessUpdatePreservesOmittedGrantsAndSiteRestriction(t *testing.T) {
	service, ids, pool := newOrgWith(t, RoleOwner, RoleViewer)
	ctx := context.Background()
	owner := principal(ids[RoleOwner], RoleOwner, ids["org"])
	siteIDs := []int32{accessTestSite(t, pool, ids["org"], "accesssite01")}
	caps := []string{"profiles.read", "data.export"}
	billing := true
	if err := service.UpdateAccess(ctx, owner, ids[RoleViewer], AccessUpdate{SiteIDs: &siteIDs, Capabilities: &caps, BillingAdmin: &billing}); err != nil {
		t.Fatal(err)
	}
	editor := RoleEditor
	if err := service.UpdateAccess(ctx, owner, ids[RoleViewer], AccessUpdate{Role: &editor}); err != nil {
		t.Fatal(err)
	}
	got, err := service.MembershipsFor(ctx, ids[RoleViewer])
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Role != RoleEditor || !reflect.DeepEqual(got[0].SiteIDs, siteIDs) || !reflect.DeepEqual(got[0].Capabilities, caps) || !got[0].BillingAdmin {
		t.Fatalf("updated membership = %+v", got)
	}
	if err := service.SetRole(ctx, owner, ids[RoleViewer], RoleViewer, siteIDs); err != nil {
		t.Fatal(err)
	}
	got, err = service.MembershipsFor(ctx, ids[RoleViewer])
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].BillingAdmin || !reflect.DeepEqual(got[0].Capabilities, caps) {
		t.Fatalf("legacy role change lost grants: %+v", got[0])
	}
}

func TestAccessUpdateDistinguishesAllAndNoSites(t *testing.T) {
	service, ids, _ := newOrgWith(t, RoleOwner, RoleViewer)
	ctx := context.Background()
	owner := principal(ids[RoleOwner], RoleOwner, ids["org"])
	noSites := []int32{}
	if err := service.UpdateAccess(ctx, owner, ids[RoleViewer], AccessUpdate{SiteIDs: &noSites}); err != nil {
		t.Fatal(err)
	}
	got, err := service.MembershipsFor(ctx, ids[RoleViewer])
	if err != nil {
		t.Fatal(err)
	}
	if got[0].SiteIDs == nil {
		t.Fatal("no-sites grant became unrestricted")
	}
	var allSites []int32
	if err := service.UpdateAccess(ctx, owner, ids[RoleViewer], AccessUpdate{SiteIDs: &allSites}); err != nil {
		t.Fatal(err)
	}
	got, err = service.MembershipsFor(ctx, ids[RoleViewer])
	if err != nil {
		t.Fatal(err)
	}
	if got[0].SiteIDs != nil {
		t.Fatalf("all-sites grant = %v", got[0].SiteIDs)
	}
}

func TestAccessUpdateRejectsEscalationAndForeignSites(t *testing.T) {
	service, ids, pool := newOrgWith(t, RoleOwner, RoleAdmin, RoleViewer)
	ctx := context.Background()
	owner := principal(ids[RoleOwner], RoleOwner, ids["org"])
	admin := principal(ids[RoleAdmin], RoleAdmin, ids["org"])
	billing := true
	if err := service.UpdateAccess(ctx, admin, ids[RoleAdmin], AccessUpdate{BillingAdmin: &billing}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin billing escalation = %v", err)
	}
	caps := []string{"billing.manage"}
	if err := service.UpdateAccess(ctx, owner, ids[RoleViewer], AccessUpdate{Capabilities: &caps}); !errors.Is(err, ErrInvalidAccess) {
		t.Fatalf("invalid additive grant = %v", err)
	}
	var otherOrg int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Other','other-access') returning id`).Scan(&otherOrg); err != nil {
		t.Fatal(err)
	}
	foreign := []int32{accessTestSite(t, pool, otherOrg, "foreign00001")}
	for _, role := range []Role{RoleViewer, RoleAdmin} {
		if err := service.UpdateAccess(ctx, owner, ids[RoleViewer], AccessUpdate{Role: &role, SiteIDs: &foreign}); !errors.Is(err, ErrInvalidAccess) {
			t.Fatalf("foreign sites for %s = %v", role, err)
		}
	}
	if err := service.UpdateAccess(ctx, owner, ids[RoleViewer], AccessUpdate{BillingAdmin: &billing}); err != nil {
		t.Fatal(err)
	}
	billing = false
	if err := service.UpdateAccess(ctx, admin, ids[RoleViewer], AccessUpdate{BillingAdmin: &billing}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin billing revocation = %v", err)
	}
	if err := service.Remove(ctx, admin, ids[RoleViewer]); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin removes billing assignment = %v", err)
	}
	if err := service.SetRole(ctx, owner, ids[RoleAdmin], RoleViewer, nil); err != nil {
		t.Fatal(err)
	}
	caps = []string{"profiles.read"}
	if err := service.UpdateAccess(ctx, admin, ids[RoleViewer], AccessUpdate{Capabilities: &caps}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale manager grants = %v", err)
	}
}

func TestAccessUpdateAuditsAndRollsBackGrants(t *testing.T) {
	service, ids, pool := newOrgWith(t, RoleOwner, RoleViewer)
	ctx := context.Background()
	owner := principal(ids[RoleOwner], RoleOwner, ids["org"])
	caps := []string{"replay.read"}
	if err := service.UpdateAccess(ctx, owner, ids[RoleViewer], AccessUpdate{Capabilities: &caps}); err != nil {
		t.Fatal(err)
	}
	var recorded []string
	if err := pool.QueryRow(ctx, `select array(select jsonb_array_elements_text(metadata->'capabilities')) from audit_log where org_id=$1`, ids["org"]).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recorded, caps) {
		t.Fatalf("audited capabilities = %v", recorded)
	}
	if _, err := pool.Exec(ctx, `create function reject_grant_audit() returns trigger language plpgsql as $$ begin raise exception 'audit unavailable'; end $$; create trigger reject_grant_audit before insert on audit_log for each row execute function reject_grant_audit()`); err != nil {
		t.Fatal(err)
	}
	caps = []string{}
	if err := service.UpdateAccess(ctx, owner, ids[RoleViewer], AccessUpdate{Capabilities: &caps}); err == nil {
		t.Fatal("grant update succeeded without audit")
	}
	got, err := service.MembershipsFor(ctx, ids[RoleViewer])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got[0].Capabilities, []string{"replay.read"}) {
		t.Fatalf("grant update was not rolled back: %+v", got[0])
	}
}

func TestBillingOnlyMembershipHasNoProjectAccess(t *testing.T) {
	service, ids, _ := newOrgWith(t, RoleOwner, RoleViewer)
	ctx := context.Background()
	noSites := []int32{}
	billing := true
	if err := service.UpdateAccess(ctx, principal(ids[RoleOwner], RoleOwner, ids["org"]), ids[RoleViewer], AccessUpdate{SiteIDs: &noSites, BillingAdmin: &billing}); err != nil {
		t.Fatal(err)
	}
	memberships, err := service.MembershipsFor(ctx, ids[RoleViewer])
	if err != nil {
		t.Fatal(err)
	}
	m := memberships[0]
	p := authz.Principal{UserID: m.UserID, OrgID: m.OrgID, Role: m.Role, SiteIDs: m.SiteIDs, Capabilities: m.Capabilities, BillingAdmin: m.BillingAdmin}
	if !authz.Can(p, authz.ActionBillingManage, 0) {
		t.Fatal("billing assignment unavailable")
	}
	if authz.Can(p, authz.ActionStatsRead, 1) || authz.Can(p, authz.ActionProfilesRead, 1) || authz.Can(p, authz.ActionReplayRead, 1) {
		t.Fatal("billing-only membership has project access")
	}
}

func accessTestSite(t *testing.T, pool *pgxpool.Pool, orgID int64, publicID string) int32 {
	t.Helper()
	var id int32
	if err := pool.QueryRow(context.Background(), `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,$2,'Site','UTC',30) returning id`, orgID, publicID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMembershipDatabaseRejectsInvalidCapabilities(t *testing.T) {
	_, ids, pool := newOrgWith(t, RoleOwner, RoleViewer)
	ctx := context.Background()
	for _, grant := range []string{`ARRAY['billing.manage']::text[]`, `ARRAY[NULL]::text[]`, `NULL`} {
		if _, err := pool.Exec(ctx, `update memberships set capabilities=`+grant+` where org_id=$1 and user_id=$2`, ids["org"], ids[RoleViewer]); err == nil {
			t.Fatalf("database accepted invalid capabilities: %s", grant)
		}
	}
}

func TestConcurrentOwnerDemotionsPreserveOneOwner(t *testing.T) {
	service, ids, _ := newOrgWith(t, RoleOwner, RoleAdmin)
	ctx := context.Background()
	owner := principal(ids[RoleOwner], RoleOwner, ids["org"])
	if err := service.SetRole(ctx, owner, ids[RoleAdmin], RoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	second := principal(ids[RoleAdmin], RoleOwner, ids["org"])
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, actor := range []authz.Principal{owner, second} {
		go func(actor authz.Principal) {
			<-start
			results <- service.SetRole(ctx, actor, actor.UserID, RoleViewer, nil)
		}(actor)
	}
	close(start)
	success, last := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, ErrLastOwner) {
			last++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || last != 1 {
		t.Fatalf("concurrent demotions: succeeded=%d last-owner-denials=%d", success, last)
	}
}
