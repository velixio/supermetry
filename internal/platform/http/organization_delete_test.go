package platformhttp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/db"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/keys"
	"supermetry/internal/platform/orgs"
	"supermetry/internal/platform/shares"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

type orgDeleteFixture struct {
	data      *store.Store
	registry  *sitesvc.Registry
	sites     *sitesvc.Sites
	principal authz.Principal
	orgID     int64
	userID    int64
}

func newOrgDeleteFixture(t *testing.T) orgDeleteFixture {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('org-owner@example.test','Owner','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Lifecycle','lifecycle') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	return orgDeleteFixture{
		data: data, registry: registry, sites: sitesvc.New(data, registry),
		principal: authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner},
		orgID:     orgID, userID: userID,
	}
}

func (f orgDeleteFixture) createSite(t *testing.T, name string) int32 {
	t.Helper()
	site, err := f.sites.Create(context.Background(), f.principal, sitesvc.CreateInput{Name: name, Hosts: []string{name + ".example.test"}, Timezone: "UTC", RetentionDays: 30, RevenueCurrency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	return site.ID
}

func TestDeleteOrgTombstonesSitesAndRevokesTenantAccess(t *testing.T) {
	f := newOrgDeleteFixture(t)
	ctx := context.Background()
	firstSite := f.createSite(t, "first")
	secondSite := f.createSite(t, "second")

	keyService := keys.New(f.data)
	t.Cleanup(keyService.Close)
	plainKey, _, err := keyService.Create(ctx, f.principal, "reporting", []string{"stats:read"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	shareService := shares.New(f.data)
	shareSlug, _, err := shareService.Create(ctx, f.principal, firstSite, "")
	if err != nil {
		t.Fatal(err)
	}
	var reportID int64
	err = f.data.Tx(ctx, func(tx pgx.Tx) error {
		var created bool
		var subscribeErr error
		reportID, created, subscribeErr = store.SubscribeReport(ctx, tx, f.orgID, f.userID, firstSite, "weekly")
		if subscribeErr != nil {
			return subscribeErr
		}
		if !created {
			return errors.New("report subscription was not created")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var foreignOrg int64
	if err := f.data.Pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Foreign','foreign') returning id`).Scan(&foreignOrg); err != nil {
		t.Fatal(err)
	}
	var foreignSite int32
	if err := f.data.Pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,revenue_currency,revenue_currency_state) values ($1,'foreign00001','Foreign','UTC',30,'USD','configured') returning id`, foreignOrg).Scan(&foreignSite); err != nil {
		t.Fatal(err)
	}

	service := Services{Store: f.data, Sites: f.sites}
	if err := service.DeleteOrg(ctx, f.principal, foreignOrg); !errors.Is(err, sitesvc.ErrNotFound) {
		t.Fatalf("cross-tenant organization delete error=%v, want not found", err)
	}
	if err := service.DeleteOrg(ctx, f.principal, f.orgID); err != nil {
		t.Fatal(err)
	}

	var orgDeletedAt time.Time
	if err := f.data.Pool.QueryRow(ctx, `select deleted_at from orgs where id=$1`, f.orgID).Scan(&orgDeletedAt); err != nil {
		t.Fatal(err)
	}
	rows, err := f.data.Pool.Query(ctx, `select id,deleted_at,deleted_with_org from sites where org_id=$1 order by id`, f.orgID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[int32]bool{}
	for rows.Next() {
		var siteID int32
		var deletedAt time.Time
		var deletedWithOrg bool
		if err := rows.Scan(&siteID, &deletedAt, &deletedWithOrg); err != nil {
			t.Fatal(err)
		}
		if !deletedAt.Equal(orgDeletedAt) {
			t.Fatalf("site %d deleted_at=%s, org deleted_at=%s", siteID, deletedAt, orgDeletedAt)
		}
		if !deletedWithOrg {
			t.Fatalf("site %d deleted_with_org=false, want true", siteID)
		}
		seen[siteID] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !seen[firstSite] || !seen[secondSite] || len(seen) != 2 {
		t.Fatalf("tombstoned sites=%v", seen)
	}
	if _, ok := f.registry.Site(firstSite); ok {
		t.Fatal("deleted organization site remained in registry")
	}
	if _, ok := keyService.Verify(ctx, plainKey); ok {
		t.Fatal("deleted organization API key remained valid")
	}
	if _, ok := shareService.Principal(ctx, shareSlug, ""); ok {
		t.Fatal("deleted organization share remained valid")
	}
	memberships, err := orgs.New(f.data).MembershipsFor(ctx, f.userID)
	if err != nil || len(memberships) != 0 {
		t.Fatalf("memberships after delete=%v err=%v", memberships, err)
	}
	deletedOrgs, err := store.DeletedOrgsForOwner(ctx, f.data.Pool, f.userID, time.Now())
	if err != nil || len(deletedOrgs) != 1 || deletedOrgs[0].ID != f.orgID {
		t.Fatalf("deleted organizations=%v err=%v", deletedOrgs, err)
	}
	reports, err := store.ReportSubscriptions(ctx, f.data)
	if err != nil || len(reports) != 0 {
		t.Fatalf("deliverable reports after delete=%v err=%v", reports, err)
	}

	var foreignDeleted bool
	if err := f.data.Pool.QueryRow(ctx, `select deleted_at is not null from sites where id=$1`, foreignSite).Scan(&foreignDeleted); err != nil || foreignDeleted {
		t.Fatalf("foreign site deleted=%t err=%v", foreignDeleted, err)
	}
	var retainedArtifacts int
	if err := f.data.Pool.QueryRow(ctx, `select
		(select count(*) from api_keys where org_id=$1) +
		(select count(*) from shared_links where site_id=$2) +
		(select count(*) from report_subscriptions where id=$3)`, f.orgID, firstSite, reportID).Scan(&retainedArtifacts); err != nil {
		t.Fatal(err)
	}
	if retainedArtifacts != 3 {
		t.Fatalf("retained access artifacts=%d, want 3 for background lifecycle", retainedArtifacts)
	}
	var siteCount int
	var scheduled bool
	if err := f.data.Pool.QueryRow(ctx, `select (metadata->>'sites_tombstoned')::integer,(metadata->>'scheduled')::boolean from audit_log where action='organization.deleted' and target_id=$1`, f.orgID).Scan(&siteCount, &scheduled); err != nil || siteCount != 2 || !scheduled {
		t.Fatalf("audit sites_tombstoned=%d scheduled=%t err=%v", siteCount, scheduled, err)
	}
}

func TestDeleteOrgKeepsEarlierSiteLifecycle(t *testing.T) {
	f := newOrgDeleteFixture(t)
	ctx := context.Background()
	service := Services{Store: f.data, Sites: f.sites}
	priorDeletedAt := time.Now().UTC().Add(-3 * 24 * time.Hour).Truncate(time.Microsecond)
	var priorSiteID int32
	if err := f.data.Pool.QueryRow(ctx, `
		insert into sites(org_id,public_id,name,timezone,retention_class,deleted_at)
		values ($1,'prior-delete','Prior','UTC',30,$2) returning id`, f.orgID, priorDeletedAt).Scan(&priorSiteID); err != nil {
		t.Fatal(err)
	}
	liveSiteID := f.createSite(t, "live")

	if err := service.DeleteOrg(ctx, f.principal, f.orgID); err != nil {
		t.Fatal(err)
	}
	var gotPriorDeletedAt time.Time
	var priorWithOrg, liveWithOrg bool
	if err := f.data.Pool.QueryRow(ctx, `select deleted_at,deleted_with_org from sites where id=$1`, priorSiteID).Scan(&gotPriorDeletedAt, &priorWithOrg); err != nil {
		t.Fatal(err)
	}
	if err := f.data.Pool.QueryRow(ctx, `select deleted_with_org from sites where id=$1`, liveSiteID).Scan(&liveWithOrg); err != nil {
		t.Fatal(err)
	}
	if !gotPriorDeletedAt.Equal(priorDeletedAt) || priorWithOrg || !liveWithOrg {
		t.Fatalf("prior deleted_at=%s with_org=%t, live with_org=%t", gotPriorDeletedAt, priorWithOrg, liveWithOrg)
	}
}

func TestDeleteOrgWithoutLiveRealSitesIsImmediate(t *testing.T) {
	tests := []struct {
		name string
		demo bool
	}{
		{name: "no sites"},
		{name: "demo only", demo: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newOrgDeleteFixture(t)
			ctx := context.Background()
			if test.demo {
				if _, err := f.data.Pool.Exec(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,is_demo) values ($1,'onlydemo0001','Demo','UTC',90,true)`, f.orgID); err != nil {
					t.Fatal(err)
				}
			}
			service := Services{Store: f.data, Sites: f.sites}
			if err := service.DeleteOrg(ctx, f.principal, f.orgID); err != nil {
				t.Fatal(err)
			}
			var scheduled bool
			if err := f.data.Pool.QueryRow(ctx, `select coalesce((metadata->>'scheduled')::boolean,false) from audit_log where action='organization.deleted' and target_id=$1`, f.orgID).Scan(&scheduled); err != nil {
				t.Fatal(err)
			}
			if scheduled {
				t.Fatal("immediate organization deletion audited as scheduled")
			}
			if test.demo {
				var deletedWithOrg bool
				if err := f.data.Pool.QueryRow(ctx, `select deleted_with_org from sites where org_id=$1`, f.orgID).Scan(&deletedWithOrg); err != nil {
					t.Fatal(err)
				}
				if deletedWithOrg {
					t.Fatal("demo site was marked deleted_with_org")
				}
			}
		})
	}
}

func TestDeleteOrgWithOnlyPreviouslyDeletedRealSiteIsScheduled(t *testing.T) {
	f := newOrgDeleteFixture(t)
	ctx := context.Background()
	deletedAt := time.Now().UTC().Add(-48 * time.Hour)
	if _, err := f.data.Pool.Exec(ctx, `
		insert into sites(org_id,public_id,name,timezone,retention_class,deleted_at)
		values ($1,'prior-only01','Prior only','UTC',30,$2)`, f.orgID, deletedAt); err != nil {
		t.Fatal(err)
	}

	if err := (Services{Store: f.data, Sites: f.sites}).DeleteOrg(ctx, f.principal, f.orgID); err != nil {
		t.Fatal(err)
	}
	var scheduled bool
	if err := f.data.Pool.QueryRow(ctx, `select (metadata->>'scheduled')::boolean from audit_log where action='organization.deleted' and target_id=$1`, f.orgID).Scan(&scheduled); err != nil {
		t.Fatal(err)
	}
	if !scheduled {
		t.Fatal("organization with unerased real data was deleted immediately")
	}
}

func TestDeleteOrgRevalidatesOwnerAndRollsBackWithAudit(t *testing.T) {
	f := newOrgDeleteFixture(t)
	ctx := context.Background()
	siteID := f.createSite(t, "rollback")
	service := Services{Store: f.data, Sites: f.sites}

	if _, err := f.data.Pool.Exec(ctx, `update memberships set role='admin' where org_id=$1 and user_id=$2`, f.orgID, f.userID); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteOrg(ctx, f.principal, f.orgID); !errors.Is(err, orgs.ErrForbidden) {
		t.Fatalf("stale owner delete error=%v, want forbidden", err)
	}
	if _, err := f.data.Pool.Exec(ctx, `update memberships set role='owner' where org_id=$1 and user_id=$2`, f.orgID, f.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.data.Pool.Exec(ctx, `
		create function reject_org_delete_audit() returns trigger language plpgsql as $$
		begin
			if new.action='organization.deleted' then raise exception 'audit unavailable'; end if;
			return new;
		end $$;
		create trigger reject_org_delete_audit before insert on audit_log
		for each row execute function reject_org_delete_audit()`); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteOrg(ctx, f.principal, f.orgID); err == nil {
		t.Fatal("organization deletion succeeded despite audit failure")
	}
	var orgDeleted, siteDeleted bool
	if err := f.data.Pool.QueryRow(ctx, `select deleted_at is not null from orgs where id=$1`, f.orgID).Scan(&orgDeleted); err != nil {
		t.Fatal(err)
	}
	if err := f.data.Pool.QueryRow(ctx, `select deleted_at is not null from sites where id=$1`, siteID).Scan(&siteDeleted); err != nil {
		t.Fatal(err)
	}
	if orgDeleted || siteDeleted {
		t.Fatalf("failed audited deletion committed org=%t site=%t", orgDeleted, siteDeleted)
	}
}

func TestDeleteOrgSerializesConcurrentSiteCreation(t *testing.T) {
	for attempt := 0; attempt < 12; attempt++ {
		t.Run(fmt.Sprint(attempt), func(t *testing.T) {
			f := newOrgDeleteFixture(t)
			service := Services{Store: f.data, Sites: f.sites}
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			var createErr, deleteErr error
			go func() {
				defer wg.Done()
				<-start
				_, createErr = f.sites.Create(context.Background(), f.principal, sitesvc.CreateInput{Name: "racing", Hosts: []string{"racing.example.test"}, Timezone: "UTC", RetentionDays: 30, RevenueCurrency: "USD"})
			}()
			go func() {
				defer wg.Done()
				<-start
				deleteErr = service.DeleteOrg(context.Background(), f.principal, f.orgID)
			}()
			close(start)
			wg.Wait()
			if deleteErr != nil {
				t.Fatal(deleteErr)
			}
			if createErr != nil && !errors.Is(createErr, sitesvc.ErrForbidden) {
				t.Fatalf("concurrent create error=%v", createErr)
			}
			var active int
			if err := f.data.Pool.QueryRow(context.Background(), `select count(*) from sites where org_id=$1 and deleted_at is null`, f.orgID).Scan(&active); err != nil {
				t.Fatal(err)
			}
			if active != 0 {
				t.Fatalf("active child sites after organization delete=%d", active)
			}
		})
	}
}
