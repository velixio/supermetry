package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

func TestUpdateUserProtectsLastAdminAndAuditsMutation(t *testing.T) {
	service, data, actorID, memberID := newFixture(t)
	ctx := context.Background()

	disabled := true
	if err := service.UpdateUser(ctx, actorID, actorID, UserPatch{Disabled: &disabled}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("disable last admin error = %v, want ErrLastAdmin", err)
	}
	promoted := true
	if err := service.UpdateUser(ctx, actorID, memberID, UserPatch{InstanceAdmin: &promoted}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateUser(ctx, memberID, actorID, UserPatch{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	var isAdmin bool
	var disabledAt *time.Time
	if err := data.Pool.QueryRow(ctx, `select is_admin,disabled_at from users where id=$1`, actorID).Scan(&isAdmin, &disabledAt); err != nil {
		t.Fatal(err)
	}
	if !isAdmin || disabledAt == nil {
		t.Fatalf("updated user is_admin=%t disabled_at=%v", isAdmin, disabledAt)
	}
	var actions []string
	rows, err := data.Pool.Query(ctx, `select action from audit_log where org_id is null order by id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	if len(actions) != 2 || actions[0] != "instance.user.updated" || actions[1] != "instance.user.updated" {
		t.Fatalf("audit actions = %v", actions)
	}
}

func TestUpdatePolicyPreventsInstanceWideLockout(t *testing.T) {
	service, data, actorID, _ := newFixture(t)
	ctx := context.Background()
	policy := TwoFactorPolicy{Scope: ScopeInstanceAdmins, GraceDays: 0}
	if err := service.UpdateTwoFactorPolicy(ctx, actorID, policy); !errors.Is(err, ErrWouldLockOut) {
		t.Fatalf("unenrolled policy error = %v, want ErrWouldLockOut", err)
	}
	if _, err := data.Pool.Exec(ctx, `update users set totp_secret_enc='x',totp_enabled=true where id=$1`, actorID); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateTwoFactorPolicy(ctx, actorID, policy); err != nil {
		t.Fatal(err)
	}
	got, err := service.TwoFactorPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != policy {
		t.Fatalf("policy = %+v, want %+v", got, policy)
	}
}

func TestTwoFactorStatusStartsGraceAndHonorsExemption(t *testing.T) {
	service, data, actorID, memberID := newFixture(t)
	ctx := context.Background()
	if _, err := data.Pool.Exec(ctx, `update users set totp_secret_enc='x',totp_enabled=true where id=$1`, actorID); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateTwoFactorPolicy(ctx, actorID, TwoFactorPolicy{Scope: ScopeEveryone, GraceDays: 2}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	status, err := service.TwoFactorStatus(ctx, memberID, now)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Required || !status.SetupRequired || status.GraceUntil == nil || !status.GraceUntil.Equal(now.Add(48*time.Hour)) {
		t.Fatalf("status = %+v", status)
	}
	status, err = service.TwoFactorStatus(ctx, memberID, now.Add(49*time.Hour))
	if err != nil || !status.SetupRequired || !status.GraceExpired {
		t.Fatalf("expired status = %+v, %v", status, err)
	}
	if err := service.SetExemption(ctx, actorID, memberID, "hardware replacement", nil); err != nil {
		t.Fatal(err)
	}
	status, err = service.TwoFactorStatus(ctx, memberID, now.Add(49*time.Hour))
	if err != nil || !status.Exempt || status.SetupRequired {
		t.Fatalf("exempt status = %+v, %v", status, err)
	}
}

func TestListSitesReturnsPublishedStorageSnapshot(t *testing.T) {
	service, data, _, _ := newFixture(t)
	ctx := context.Background()
	var orgID int64
	if err := data.Pool.QueryRow(ctx, `select id from orgs where slug='acme'`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	siteID := insertAdminTestSite(t, data, orgID, "storage00001", "Storage site")
	measuredAt := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	insertAdminTestStorage(t, data, siteID, [5]int64{11, 22, 33, 44, 55}, measuredAt, 1)

	sites, err := service.ListSites(ctx, "Storage site", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 {
		t.Fatalf("sites = %+v, want one", sites)
	}
	storage := sites[0].Storage
	if storage == nil {
		t.Fatal("storage = nil, want published snapshot")
	}
	if storage.EstimatedStorageBytes != 165 || storage.DatabaseBytes != 66 || storage.ReplayBytes != 99 ||
		storage.HotBytes == nil || *storage.HotBytes != 11 ||
		storage.RetainedBytes == nil || *storage.RetainedBytes != 22 ||
		storage.AuxiliaryBytes == nil || *storage.AuxiliaryBytes != 33 ||
		storage.ReplayRecordingBytes == nil || *storage.ReplayRecordingBytes != 44 ||
		storage.ReplayCSSBytes == nil || *storage.ReplayCSSBytes != 55 ||
		!storage.MeasuredAt.Equal(measuredAt) || storage.MeasurementVersion != 1 {
		t.Fatalf("storage = %+v", storage)
	}
}

func TestOrganizationStorageAggregatesAllNonErasedSites(t *testing.T) {
	service, data, _, _ := newFixture(t)
	ctx := context.Background()
	measuredAt := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

	completeOrg := insertAdminTestOrg(t, data, "Complete", "storage-complete")
	active := insertAdminTestSite(t, data, completeOrg, "storage00002", "Active")
	restorable := insertAdminTestSite(t, data, completeOrg, "storage00003", "Restorable")
	erased := insertAdminTestSite(t, data, completeOrg, "storage00004", "Erased")
	if _, err := data.Pool.Exec(ctx, `update sites set deleted_at=$2 where id=$1`, restorable, measuredAt.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := data.Pool.Exec(ctx, `update sites set deleted_at=$2,erased_at=$2 where id=$1`, erased, measuredAt.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	insertAdminTestStorage(t, data, active, [5]int64{10, 20, 30, 40, 50}, measuredAt, 1)
	insertAdminTestStorage(t, data, restorable, [5]int64{1, 2, 3, 4, 5}, measuredAt, 1)
	insertAdminTestStorage(t, data, erased, [5]int64{900, 900, 900, 900, 900}, measuredAt, 1)

	unknownOrg := insertAdminTestOrg(t, data, "Unknown", "storage-unknown")
	known := insertAdminTestSite(t, data, unknownOrg, "storage00005", "Known")
	insertAdminTestSite(t, data, unknownOrg, "storage00006", "Missing")
	insertAdminTestStorage(t, data, known, [5]int64{7, 8, 9, 10, 11}, measuredAt, 1)

	nothingOrg := insertAdminTestOrg(t, data, "Nothing", "storage-nothing")
	insertAdminTestSite(t, data, nothingOrg, "storage00008", "Nothing yet")

	foreignOrg := insertAdminTestOrg(t, data, "Foreign", "storage-foreign")
	foreign := insertAdminTestSite(t, data, foreignOrg, "storage00007", "Foreign")
	insertAdminTestStorage(t, data, foreign, [5]int64{100, 200, 300, 400, 500}, measuredAt, 1)

	organizations, err := service.ListOrganizations(ctx, "storage-", 20, 10)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[int64]Organization, len(organizations))
	for _, organization := range organizations {
		byID[organization.ID] = organization
	}
	complete := byID[completeOrg].Storage
	if complete == nil {
		t.Fatal("complete organization storage = nil")
	}
	if complete.EstimatedStorageBytes != 165 || complete.DatabaseBytes != 66 || complete.ReplayBytes != 99 || !complete.MeasuredAt.Equal(measuredAt) {
		t.Fatalf("complete organization storage = %+v", complete)
	}
	// A site created after the run's site capture has no snapshot yet; the
	// measured sites are still totalled and the total is flagged as partial.
	partial := byID[unknownOrg].Storage
	if partial == nil || partial.EstimatedStorageBytes != 45 || partial.UnmeasuredSites != 1 || !partial.MeasuredAt.Equal(measuredAt) {
		t.Fatalf("partially measured organization storage = %+v, want 45 bytes with 1 unmeasured site", partial)
	}
	if complete.UnmeasuredSites != 0 {
		t.Fatalf("complete organization unmeasured sites = %d, want 0", complete.UnmeasuredSites)
	}
	if byID[nothingOrg].Storage != nil {
		t.Fatalf("organization with no snapshot at all storage = %+v, want nil (Calculating)", byID[nothingOrg].Storage)
	}
	if got := byID[foreignOrg].Storage; got == nil || got.EstimatedStorageBytes != 1500 {
		t.Fatalf("foreign organization storage = %+v, want isolated total 1500", got)
	}

	detail, err := service.GetOrganization(ctx, completeOrg, 10)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Storage == nil || detail.Storage.EstimatedStorageBytes != 165 {
		t.Fatalf("organization detail storage = %+v, want 165", detail.Storage)
	}
	partialDetail, err := service.GetOrganization(ctx, unknownOrg, 10)
	if err != nil {
		t.Fatal(err)
	}
	if partialDetail.Storage == nil || partialDetail.Storage.EstimatedStorageBytes != 45 || partialDetail.Storage.UnmeasuredSites != 1 {
		t.Fatalf("partial organization detail storage = %+v, want 45 with 1 unmeasured", partialDetail.Storage)
	}
}

func TestGetOrganizationReturnsOwnRestorableSitesAfterFiltering(t *testing.T) {
	service, data, _, _ := newFixture(t)
	ctx := context.Background()
	foreignOrg := insertAdminTestOrg(t, data, "Foreign sites", "detail-foreign")
	if _, err := data.Pool.Exec(ctx, `
		insert into sites(org_id,public_id,name,timezone,retention_class)
		select $1,'foreign'||lpad(n::text,5,'0'),'Foreign '||n,'UTC',30
		from generate_series(1,100) n`, foreignOrg); err != nil {
		t.Fatal(err)
	}

	targetOrg := insertAdminTestOrg(t, data, "Restorable org", "detail-restorable")
	targetSite := insertAdminTestSite(t, data, targetOrg, "detailsite01", "Restorable site")
	deletedAt := time.Date(2026, 10, 26, 11, 0, 0, 0, time.UTC)
	if _, err := data.Pool.Exec(ctx, `update orgs set deleted_at=$2 where id=$1`, targetOrg, deletedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := data.Pool.Exec(ctx, `update sites set deleted_at=$2,deleted_with_org=true where id=$1`, targetSite, deletedAt); err != nil {
		t.Fatal(err)
	}
	insertAdminTestStorage(t, data, targetSite, [5]int64{1, 2, 3, 4, 5}, deletedAt.Add(-time.Hour), 1)

	detail, err := service.GetOrganization(ctx, targetOrg, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Sites) != 1 || detail.Sites[0].ID != targetSite {
		t.Fatalf("detail sites = %+v, want only restorable site %d", detail.Sites, targetSite)
	}
	if storage := detail.Sites[0].Storage; storage == nil || storage.EstimatedStorageBytes != 15 || storage.HotBytes == nil || *storage.HotBytes != 1 {
		t.Fatalf("restorable site storage = %+v", storage)
	}
}

func TestOrganizationWithoutSitesUsesLatestCompletedGeneration(t *testing.T) {
	service, data, _, _ := newFixture(t)
	ctx := context.Background()
	orgID := insertAdminTestOrg(t, data, "Empty", "empty-storage")

	before, err := service.GetOrganization(ctx, orgID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if before.Storage != nil {
		t.Fatalf("storage before a completed generation = %+v, want nil", before.Storage)
	}

	completedAt := time.Date(2026, 10, 26, 13, 0, 0, 0, time.UTC)
	if _, err := data.Pool.Exec(ctx, `
		insert into storage_usage_runs(status,measurement_version,work_fingerprint,started_at,updated_at,completed_at)
		values ('complete',2,'older',$1,$1,$1),('complete',3,'latest',$2,$2,$2)`, completedAt.Add(-time.Hour), completedAt); err != nil {
		t.Fatal(err)
	}
	after, err := service.GetOrganization(ctx, orgID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if after.Storage == nil || after.Storage.EstimatedStorageBytes != 0 || after.Storage.DatabaseBytes != 0 || after.Storage.ReplayBytes != 0 ||
		!after.Storage.MeasuredAt.Equal(completedAt) || after.Storage.MeasurementVersion != 3 {
		t.Fatalf("storage after completed generation = %+v, want zero at %s version 3", after.Storage, completedAt)
	}
	organizations, err := service.ListOrganizations(ctx, "empty-storage", 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(organizations) != 1 || organizations[0].Storage == nil || organizations[0].Storage.EstimatedStorageBytes != 0 ||
		!organizations[0].Storage.MeasuredAt.Equal(completedAt) || organizations[0].Storage.MeasurementVersion != 3 {
		t.Fatalf("listed empty organization = %+v, want latest generation zero", organizations)
	}
}

func TestAdminStorageQueriesReadPublishedSnapshots(t *testing.T) {
	_, data, _, _ := newFixture(t)
	ctx := context.Background()
	for name, query := range map[string]string{
		"organizations":      listOrganizationsSQL,
		"organization":       getOrganizationSQL,
		"sites":              listSitesSQL,
		"organization sites": getOrganizationSitesSQL,
	} {
		t.Run(name, func(t *testing.T) {
			args := []any{"", 10}
			switch name {
			case "organization":
				args = []any{int64(1)}
			case "organization sites":
				args = []any{int64(1), MaxListLimit}
			}
			var planJSON []byte
			if err := data.Pool.QueryRow(ctx, "explain (format json) "+query, args...).Scan(&planJSON); err != nil {
				t.Fatal(err)
			}
			var plan any
			if err := json.Unmarshal(planJSON, &plan); err != nil {
				t.Fatal(err)
			}
			encoded := fmt.Sprintf("%v", plan)
			if !strings.Contains(encoded, "site_storage_usage") {
				t.Fatalf("plan does not read published storage snapshots: %s", planJSON)
			}
			for _, forbidden := range []string{" events ", " sessions ", "replay_sessions", "pg_column_size"} {
				if strings.Contains(" "+encoded+" ", forbidden) {
					t.Fatalf("plan contains live storage source %q: %s", strings.TrimSpace(forbidden), planJSON)
				}
			}
		})
	}
}

func insertAdminTestOrg(t *testing.T, data *store.Store, name, slug string) int64 {
	t.Helper()
	var id int64
	if err := data.Pool.QueryRow(context.Background(), `insert into orgs(name,slug) values ($1,$2) returning id`, name, slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertAdminTestSite(t *testing.T, data *store.Store, orgID int64, publicID, name string) int32 {
	t.Helper()
	var id int32
	if err := data.Pool.QueryRow(context.Background(), `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,$2,$3,'UTC',30) returning id`, orgID, publicID, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertAdminTestStorage(t *testing.T, data *store.Store, siteID int32, bytes [5]int64, measuredAt time.Time, version int) {
	t.Helper()
	if _, err := data.Pool.Exec(context.Background(), `
		insert into site_storage_usage(
			site_id,hot_bytes,retained_bytes,auxiliary_bytes,replay_recording_bytes,replay_css_bytes,measured_at,measurement_version
		) values ($1,$2,$3,$4,$5,$6,$7,$8)`, siteID, bytes[0], bytes[1], bytes[2], bytes[3], bytes[4], measuredAt, version); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T) (*Service, *store.Store, int64, int64) {
	t.Helper()
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	var actorID, memberID int64
	if err := pool.QueryRow(context.Background(), `insert into users(email,name,password_hash,is_admin) values ('instance-admin@example.test','Instance Admin','hash',true) returning id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `insert into users(email,name,password_hash) values ('member@example.test','Member','hash') returning id`).Scan(&memberID); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(context.Background(), `insert into orgs(name,slug) values ('Acme','acme') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `insert into memberships(org_id,user_id,role) values ($1,$2,'owner'),($1,$3,'viewer')`, orgID, actorID, memberID); err != nil {
		t.Fatal(err)
	}
	return New(data), data, actorID, memberID
}

func TestGetOrganizationOmitsIndividuallyDeletedSites(t *testing.T) {
	service, data, _, _ := newFixture(t)
	ctx := context.Background()
	orgID := insertAdminTestOrg(t, data, "Live org", "detail-live")
	live := insertAdminTestSite(t, data, orgID, "livesite0001", "Live")
	removed := insertAdminTestSite(t, data, orgID, "gonesite0001", "Deleted on its own")
	if _, err := data.Pool.Exec(ctx, `update sites set deleted_at=now() where id=$1`, removed); err != nil {
		t.Fatal(err)
	}

	detail, err := service.GetOrganization(ctx, orgID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Sites) != 1 || detail.Sites[0].ID != live {
		t.Fatalf("detail sites = %+v, want only live site %d (site_count=%d)", detail.Sites, live, detail.SiteCount)
	}
	if int64(len(detail.Sites)) != detail.SiteCount {
		t.Fatalf("listed sites %d != site_count %d", len(detail.Sites), detail.SiteCount)
	}
}
