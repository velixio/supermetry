package admin

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/platform/store"
)

// walkUsers pages through every user with the given sort and limit and returns
// the ids in the order they were served.
func walkUsers(t *testing.T, s *Service, params ListParams) []int64 {
	t.Helper()
	var ids []int64
	for pages := 0; pages < 1000; pages++ {
		page, err := s.ListUsersPage(context.Background(), params)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range page.Items {
			ids = append(ids, u.ID)
		}
		if page.NextCursor == "" {
			return ids
		}
		params.Cursor = page.NextCursor
	}
	t.Fatal("paging did not terminate")
	return nil
}

func assertNoDupesNoGaps(t *testing.T, got []int64, want int) {
	t.Helper()
	seen := map[int64]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("duplicate id %d in %v", id, got)
		}
		seen[id] = true
	}
	if len(got) != want {
		t.Fatalf("got %d rows, want %d", len(got), want)
	}
}

func TestListUsersPageKeysetNoDupesNoGapsAcrossSorts(t *testing.T) {
	s, data, _, _ := newFixture(t)
	ctx := context.Background()
	// 120 users; created_at and name tie in groups of 10 so ties must be stable.
	for i := 0; i < 120; i++ {
		if _, err := data.Pool.Exec(ctx, `insert into users(email,name,password_hash,created_at) values ($1,$2,'h',$3)`,
			fmt.Sprintf("bulk%03d@example.test", i), fmt.Sprintf("Name %d", i/10), time.Date(2026, 1, 1, 0, 0, i/10, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	total := 122
	for _, sort := range []string{"", "id", "created_at", "email", "name"} {
		for _, order := range []string{"", "asc", "desc"} {
			for _, limit := range []int{7, 50, 200} {
				ids := walkUsers(t, s, ListParams{Sort: sort, Order: order, Limit: limit})
				assertNoDupesNoGaps(t, ids, total)
			}
		}
	}
	asc := walkUsers(t, s, ListParams{Sort: "created_at", Order: "asc", Limit: 13})
	desc := walkUsers(t, s, ListParams{Sort: "created_at", Order: "desc", Limit: 13})
	// Ties break by id in the same direction as the sort, so desc is the exact reverse of asc.
	for i := range asc {
		if asc[i] != desc[len(desc)-1-i] {
			t.Fatalf("desc is not the reverse of asc at %d", i)
		}
	}
}

func TestListUsersPageRejectsBadParams(t *testing.T) {
	s, _, _, _ := newFixture(t)
	ctx := context.Background()
	for _, p := range []ListParams{{Sort: "password_hash"}, {Order: "sideways"}, {Cursor: "garbage"}} {
		if _, err := s.ListUsersPage(ctx, p); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v error = %v, want ErrInvalid", p, err)
		}
	}
	page, err := s.ListUsersPage(ctx, ListParams{Sort: "email", Limit: 1})
	if err != nil || page.NextCursor == "" {
		t.Fatalf("page = %+v err=%v", page, err)
	}
	// A cursor is bound to the sort and order it was issued for.
	if _, err := s.ListUsersPage(ctx, ListParams{Sort: "name", Cursor: page.NextCursor}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatched cursor error = %v", err)
	}
}

func TestListOrganizationsPageSortsByMembersAndStorage(t *testing.T) {
	s, data, _, _ := newFixture(t)
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		var orgID int64
		if err := data.Pool.QueryRow(ctx, `insert into orgs(name,slug) values ($1,$2) returning id`, fmt.Sprintf("Org %02d", i), fmt.Sprintf("org-%02d", i)).Scan(&orgID); err != nil {
			t.Fatal(err)
		}
		for m := 0; m < i%4; m++ { // member counts tie in groups
			if _, err := data.Pool.Exec(ctx, `insert into memberships(org_id,user_id,role) select $1,u.id,'viewer' from (select id from users order by id limit 1 offset $2) u`, orgID, m); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, sort := range []string{"", "created_at", "name", "members", "storage"} {
		for _, order := range []string{"asc", "desc"} {
			seen := map[int64]bool{}
			params := ListParams{Sort: sort, Order: order, Limit: 4}
			var lastMembers int64 = -1
			count := 0
			for {
				page, err := s.ListOrganizationsPage(ctx, params, 10)
				if err != nil {
					t.Fatalf("sort=%s order=%s: %v", sort, order, err)
				}
				for _, o := range page.Items {
					if seen[o.ID] {
						t.Fatalf("sort=%s order=%s duplicate org %d", sort, order, o.ID)
					}
					seen[o.ID] = true
					count++
					if sort == "members" {
						if lastMembers >= 0 && (order == "asc" && o.MemberCount < lastMembers || order == "desc" && o.MemberCount > lastMembers) {
							t.Fatalf("members out of order: %d after %d (%s)", o.MemberCount, lastMembers, order)
						}
						lastMembers = o.MemberCount
					}
				}
				if page.NextCursor == "" {
					break
				}
				params.Cursor = page.NextCursor
			}
			if count != 26 { // 25 plus the fixture's Acme
				t.Fatalf("sort=%s order=%s saw %d orgs, want 26", sort, order, count)
			}
		}
	}
}

func TestListSitesPageKeyset(t *testing.T) {
	s, data, _, _ := newFixture(t)
	orgID := insertAdminTestOrg(t, data, "Sites org", "sites-org")
	for i := 0; i < 30; i++ {
		insertAdminTestSite(t, data, orgID, fmt.Sprintf("pagesite%04d", i), fmt.Sprintf("Site %d", i%5))
	}
	for _, sort := range []string{"", "created_at", "name", "storage"} {
		for _, order := range []string{"asc", "desc"} {
			params := ListParams{Sort: sort, Order: order, Limit: 6}
			seen := map[int32]bool{}
			for {
				page, err := s.ListSitesPage(context.Background(), params)
				if err != nil {
					t.Fatal(err)
				}
				for _, site := range page.Items {
					if seen[site.ID] {
						t.Fatalf("sort=%s order=%s duplicate site %d", sort, order, site.ID)
					}
					seen[site.ID] = true
				}
				if page.NextCursor == "" {
					break
				}
				params.Cursor = page.NextCursor
			}
			if len(seen) != 30 {
				t.Fatalf("sort=%s order=%s saw %d sites, want 30", sort, order, len(seen))
			}
		}
	}
}

func TestGetOrganizationReturnsEveryMemberBeyondFirstHundredUsers(t *testing.T) {
	s, data, _, _ := newFixture(t)
	ctx := context.Background()
	orgID := insertAdminTestOrg(t, data, "Big", "big-org")
	// 150 users created after the fixture users, all members of the org: the
	// old implementation only looked at the first 100 users overall.
	if _, err := data.Pool.Exec(ctx, `
		with u as (insert into users(email,name,password_hash) select 'big'||g||'@example.test','Big '||g,'h' from generate_series(1,150) g returning id)
		insert into memberships(org_id,user_id,role) select $1,id,'viewer' from u`, orgID); err != nil {
		t.Fatal(err)
	}
	detail, err := s.GetOrganization(ctx, orgID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Members) != 150 || detail.MemberCount != 150 {
		t.Fatalf("members=%d member_count=%d, want 150", len(detail.Members), detail.MemberCount)
	}
	for _, m := range detail.Members {
		if len(m.Memberships) != 1 || m.Memberships[0].OrgID != orgID {
			t.Fatalf("member memberships = %+v", m.Memberships)
		}
	}
	page, err := s.GetOrganizationMembers(ctx, orgID, ListParams{Limit: 60})
	if err != nil || len(page.Items) != 60 || page.NextCursor == "" {
		t.Fatalf("members page len=%d next=%q err=%v", len(page.Items), page.NextCursor, err)
	}
	rest, err := s.GetOrganizationMembers(ctx, orgID, ListParams{Limit: 200, Cursor: page.NextCursor})
	if err != nil || len(rest.Items) != 90 || rest.NextCursor != "" {
		t.Fatalf("rest len=%d next=%q err=%v", len(rest.Items), rest.NextCursor, err)
	}
}

func TestUserLookupBeyondFirstHundred(t *testing.T) {
	s, data, _, _ := newFixture(t)
	ctx := context.Background()
	var last int64
	if err := data.Pool.QueryRow(ctx, `
		with u as (insert into users(email,name,password_hash) select 'many'||g||'@example.test','M','h' from generate_series(1,150) g returning id)
		select max(id) from u`).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if _, err := s.User(ctx, last); err != nil {
		t.Fatalf("User(%d) = %v", last, err)
	}
	if _, err := s.User(ctx, last+1000); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user error = %v", err)
	}
}

func TestListAuditPageFiltersAndKeyset(t *testing.T) {
	s, data, actorID, memberID := newFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	// 40 entries; created_at repeats in pairs so the (created_at,id) tie-break matters.
	for i := 0; i < 40; i++ {
		actor, action, targetType := actorID, "instance.user.updated", "user"
		if i%2 == 1 {
			actor, action, targetType = memberID, "instance.settings.updated", "instance"
		}
		targetID := int64(i % 3)
		if i%5 == 0 {
			targetType, targetID = "organization", 77
		}
		if _, err := data.Pool.Exec(ctx, `insert into audit_log(org_id,actor_user_id,action,target_type,target_id,created_at) values (null,$1,$2,$3,$4,$5)`,
			actor, action, targetType, targetID, base.Add(time.Duration(i/2)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	// A per-org audit row must never leak into the instance log.
	var orgID int64
	if err := data.Pool.QueryRow(ctx, `select id from orgs limit 1`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := data.Pool.Exec(ctx, `insert into audit_log(org_id,actor_user_id,action,target_type,target_id) values ($1,$2,'org.thing','org',1)`, orgID, actorID); err != nil {
		t.Fatal(err)
	}
	walk := func(f AuditFilter, limit int) []AuditEntry {
		f.Limit = limit
		var out []AuditEntry
		for {
			page, err := s.ListAuditPage(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, page.Items...)
			if page.NextCursor == "" {
				return out
			}
			f.Cursor = page.NextCursor
		}
	}
	all := walk(AuditFilter{}, 7)
	if len(all) != 40 {
		t.Fatalf("all = %d, want 40", len(all))
	}
	seen := map[int64]bool{}
	for i, e := range all {
		if seen[e.ID] {
			t.Fatalf("duplicate audit id %d", e.ID)
		}
		seen[e.ID] = true
		if i > 0 && (e.CreatedAt.After(all[i-1].CreatedAt) || e.CreatedAt.Equal(all[i-1].CreatedAt) && e.ID > all[i-1].ID) {
			t.Fatalf("not newest first at %d", i)
		}
	}
	if got := walk(AuditFilter{ActorUserID: memberID}, 5); len(got) != 20 {
		t.Fatalf("actor filter = %d, want 20", len(got))
	}
	if got := walk(AuditFilter{Action: "instance.settings.updated"}, 5); len(got) != 20 {
		t.Fatalf("action filter = %d, want 20", len(got))
	}
	if got := walk(AuditFilter{Action: "instance.*"}, 9); len(got) != 40 {
		t.Fatalf("action prefix filter = %d, want 40", len(got))
	}
	if got := walk(AuditFilter{OrgID: 77}, 3); len(got) != 8 {
		t.Fatalf("org filter = %d, want 8", len(got))
	}
	from, to := base.Add(5*time.Hour), base.Add(10*time.Hour)
	got := walk(AuditFilter{From: &from, To: &to}, 4)
	if len(got) != 10 { // hours 5..9, two rows each; to is exclusive
		t.Fatalf("time filter = %d, want 10", len(got))
	}
	if _, err := s.ListAuditPage(ctx, AuditFilter{Cursor: "nope", Limit: 5}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad cursor error = %v", err)
	}
}

// localeService is the service on a pool whose sessions use a non-UTC TimeZone
// and a day-first DateStyle, which break cursors that round-trip timestamps as text.
func localeService(t *testing.T, data *store.Store) *Service {
	t.Helper()
	cfg := data.Pool.Config().Copy()
	cfg.ConnConfig.RuntimeParams["TimeZone"] = "Asia/Kolkata"
	cfg.ConnConfig.RuntimeParams["DateStyle"] = "SQL, DMY"
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return New(&store.Store{Pool: pool})
}

func TestAuditAndListCursorsSurviveNonUTCSession(t *testing.T) {
	_, data, actorID, _ := newFixture(t)
	s := localeService(t, data)
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 0, 0, 0, 123456000, time.UTC)
	for i := 0; i < 12; i++ {
		if _, err := data.Pool.Exec(ctx, `insert into audit_log(org_id,actor_user_id,action,target_type,target_id,created_at) values (null,$1,'instance.x','user',1,$2)`,
			actorID, base.Add(time.Duration(i/2)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	f := AuditFilter{Limit: 5}
	seen := map[int64]bool{}
	for pages := 0; pages < 10; pages++ {
		page, err := s.ListAuditPage(ctx, f)
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		for _, e := range page.Items {
			if seen[e.ID] {
				t.Fatalf("duplicate %d", e.ID)
			}
			seen[e.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		f.Cursor = page.NextCursor
	}
	if len(seen) != 12 {
		t.Fatalf("walked %d audit rows, want 12", len(seen))
	}
	ids := walkUsers(t, s, ListParams{Sort: "created_at", Order: "desc", Limit: 1})
	assertNoDupesNoGaps(t, ids, 2)
}

func TestForgedTimestampCursorIsInvalid(t *testing.T) {
	s, _, _, _ := newFixture(t)
	ctx := context.Background()
	forged := encodeCursor(cursorToken{Sort: "created_at", Order: "asc", Value: "not a time", ID: 1})
	if _, err := s.ListUsersPage(ctx, ListParams{Sort: "created_at", Cursor: forged}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged users cursor error = %v, want ErrInvalid", err)
	}
	forgedAudit := encodeCursor(cursorToken{Sort: "created_at", Order: "desc", Value: "2026-01-01", ID: 1})
	if _, err := s.ListAuditPage(ctx, AuditFilter{Cursor: forgedAudit}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged audit cursor error = %v, want ErrInvalid", err)
	}
}

func TestUserDetailRevokeSessionsAndAudit(t *testing.T) {
	s, data, actorID, memberID := newFixture(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := data.Pool.Exec(ctx, `insert into user_sessions(id_hash,user_id,expires_at,device_label) values (sha256(($1::text)::bytea),$2,now()+interval '1 day','dev')`, fmt.Sprint("tok", i), memberID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := data.Pool.Exec(ctx, `insert into user_sessions(id_hash,user_id,expires_at,device_label) values (sha256('expired'::bytea),$1,now()-interval '1 day','dev')`, memberID); err != nil {
		t.Fatal(err)
	}
	detail, err := s.UserDetail(ctx, memberID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ActiveSessions != 3 || len(detail.Memberships) != 1 || detail.Memberships[0].Role != "viewer" {
		t.Fatalf("detail = %+v", detail)
	}
	if _, err := s.UserDetail(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing detail error = %v", err)
	}
	if _, err := s.RevokeUserSessions(ctx, memberID, memberID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin revoke error = %v", err)
	}
	n, err := s.RevokeUserSessions(ctx, actorID, memberID)
	if err != nil || n != 4 {
		t.Fatalf("revoked = %d err=%v, want 4 (active and expired rows are all removed)", n, err)
	}
	var audits int
	if err := data.Pool.QueryRow(ctx, `select count(*) from audit_log where action='instance.user.sessions_revoked' and actor_user_id=$1 and target_id=$2 and (metadata->>'revoked')::int=4`, actorID, memberID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit rows = %d err=%v", audits, err)
	}
	if _, err := s.RevokeUserSessions(ctx, actorID, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user revoke error = %v", err)
	}
}

func TestStartPasswordResetAudited(t *testing.T) {
	s, data, actorID, memberID := newFixture(t)
	ctx := context.Background()
	if _, err := s.StartPasswordReset(ctx, actorID, memberID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no sender wired error = %v, want ErrInvalid", err)
	}
	var gotEmail string
	s.SetPasswordResetStarter(func(_ context.Context, email string) (string, error) {
		gotEmail = email
		return "https://example.test/reset/abc", nil
	})
	if _, err := s.StartPasswordReset(ctx, memberID, memberID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin error = %v", err)
	}
	if _, err := s.StartPasswordReset(ctx, actorID, actorID); !errors.Is(err, ErrAdminTarget) {
		t.Fatalf("admin target error = %v, want ErrAdminTarget", err)
	}
	if gotEmail != "" {
		t.Fatal("a reset was issued for an administrator")
	}
	res, err := s.StartPasswordReset(ctx, actorID, memberID)
	if err != nil || gotEmail != "member@example.test" || res.ResetURL != "https://example.test/reset/abc" || res.Sent {
		t.Fatalf("res=%+v email=%q err=%v", res, gotEmail, err)
	}
	s.SetPasswordResetStarter(func(context.Context, string) (string, error) { return "", nil })
	res, err = s.StartPasswordReset(ctx, actorID, memberID)
	if err != nil || !res.Sent || res.ResetURL != "" {
		t.Fatalf("mailed res=%+v err=%v", res, err)
	}
	var audits int
	if err := data.Pool.QueryRow(ctx, `select count(*) from audit_log where action='instance.user.password_reset_started' and actor_user_id=$1 and target_id=$2`, actorID, memberID).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audit rows = %d err=%v", audits, err)
	}
	if _, err := s.StartPasswordReset(ctx, actorID, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user error = %v", err)
	}
}

func TestStartPasswordResetAuditsBeforeSending(t *testing.T) {
	s, data, actorID, memberID := newFixture(t)
	ctx := context.Background()
	var auditedAtSend int
	s.SetPasswordResetStarter(func(ctx context.Context, _ string) (string, error) {
		data.Pool.QueryRow(ctx, `select count(*) from audit_log where action='instance.user.password_reset_started' and target_id=$1`, memberID).Scan(&auditedAtSend)
		return "", errors.New("smtp down")
	})
	if _, err := s.StartPasswordReset(ctx, actorID, memberID); err == nil {
		t.Fatal("want the sender error")
	}
	if auditedAtSend != 1 {
		t.Fatalf("audit rows visible when sending = %d, want 1", auditedAtSend)
	}
}

var _ = store.Store{}
