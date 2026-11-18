package platformhttp_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"supermetry/internal/platform/authz"
)

// notifyPrefsFixture adds a viewer member (own user) and a user of another org.
func newNotifyPrefsFixture(t *testing.T) (issuesFixture, int64, int64) {
	t.Helper()
	f := newAppIssuesFixture(t)
	ctx := context.Background()
	var orgID, foreignOrg, viewer, carol int64
	if err := f.pool.QueryRow(ctx, `select org_id from sites where id=$1`, f.siteID).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `select org_id from sites where id=$1`, f.foreignSite).Scan(&foreignOrg); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('viewer@example.test','Viewer','h') returning id`).Scan(&viewer); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('carol@example.test','Carol','h') returning id`).Scan(&carol); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		org, user int64
		role      string
	}{{orgID, viewer, "viewer"}, {foreignOrg, carol, "owner"}} {
		if _, err := f.pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,$3)`, m.org, m.user, m.role); err != nil {
			t.Fatal(err)
		}
	}
	f.authenticator["viewer-user"] = authz.Principal{UserID: viewer, OrgID: orgID, Role: authz.RoleViewer}
	f.authenticator["carol"] = authz.Principal{UserID: carol, OrgID: foreignOrg, Role: authz.RoleOwner}
	return f, viewer, carol
}

func (f issuesFixture) prefsFor(t *testing.T, actor string) []any {
	t.Helper()
	out := f.json(t, actor, http.MethodGet, "/api/private/me/app-notifications", ``, http.StatusOK)
	return out["sites"].([]any)
}

func TestAppNotificationPrefsDefaultsAndOwnEdits(t *testing.T) {
	f, viewer, _ := newNotifyPrefsFixture(t)
	if _, err := f.pool.Exec(context.Background(), `update sites set kind='app'`); err != nil {
		t.Fatal(err)
	}
	var found map[string]any
	for _, row := range f.prefsFor(t, "viewer-user") {
		if r := row.(map[string]any); r["site_id"] == float64(f.siteID) {
			found = r
		}
		if r := row.(map[string]any); r["site_id"] == float64(f.foreignSite) {
			t.Fatal("another organisation's project is listed")
		}
	}
	if found == nil || found["assigned"] != true || found["resolved"] != false || found["regressed"] != true {
		t.Fatalf("defaults = %v", found)
	}
	// A viewer may switch their own notifications.
	path := fmt.Sprintf("/api/private/me/app-notifications/%d", f.siteID)
	f.json(t, "viewer-user", http.MethodPut, path, `{"assigned":false,"resolved":true,"regressed":false}`, http.StatusOK)
	var a, r, g bool
	if err := f.pool.QueryRow(context.Background(), `select assigned,resolved,regressed from user_notification_prefs where user_id=$1 and site_id=$2`, viewer, f.siteID).Scan(&a, &r, &g); err != nil || a || !r || g {
		t.Fatalf("saved = %v %v %v err=%v", a, r, g, err)
	}
	// Only the caller's row changes: the owner's prefs are untouched.
	var others int
	_ = f.pool.QueryRow(context.Background(), `select count(*) from user_notification_prefs where user_id <> $1`, viewer).Scan(&others)
	if others != 0 {
		t.Fatalf("%d other users' rows exist", others)
	}
}

func TestAppNotificationPrefsWeeklyDigestIsOptionalAndOwn(t *testing.T) {
	f, viewer, _ := newNotifyPrefsFixture(t)
	if _, err := f.pool.Exec(context.Background(), `update sites set kind='app'`); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/private/me/app-notifications/%d", f.siteID)
	digest := func() bool {
		var on bool
		if err := f.pool.QueryRow(context.Background(), `select weekly_digest from user_notification_prefs where user_id=$1 and site_id=$2`, viewer, f.siteID).Scan(&on); err != nil {
			t.Fatal(err)
		}
		return on
	}
	out := f.json(t, "viewer-user", http.MethodPut, path, `{"assigned":true,"resolved":false,"regressed":true,"weekly_digest":true}`, http.StatusOK)
	if out["weekly_digest"] != true || !digest() {
		t.Fatalf("digest not switched on: %v", out)
	}
	// Saving the other switches without the field keeps the digest as it was.
	out = f.json(t, "viewer-user", http.MethodPut, path, `{"assigned":false,"resolved":false,"regressed":true}`, http.StatusOK)
	if out["weekly_digest"] != true || !digest() {
		t.Fatalf("digest lost when the field was omitted: %v", out)
	}
	for _, row := range f.prefsFor(t, "viewer-user") {
		if r := row.(map[string]any); r["site_id"] == float64(f.siteID) && r["weekly_digest"] != true {
			t.Fatalf("list does not show the digest switch: %v", r)
		}
	}
	// A project the caller cannot read stays a 404 for the digest too.
	f.json(t, "carol", http.MethodPut, path, `{"assigned":true,"resolved":true,"regressed":true,"weekly_digest":true}`, http.StatusNotFound)
}

func TestAppNotificationPrefsGuards(t *testing.T) {
	f, _, _ := newNotifyPrefsFixture(t)
	if _, err := f.pool.Exec(context.Background(), `update sites set kind='app'`); err != nil {
		t.Fatal(err)
	}
	own := fmt.Sprintf("/api/private/me/app-notifications/%d", f.siteID)
	foreign := fmt.Sprintf("/api/private/me/app-notifications/%d", f.foreignSite)
	body := `{"assigned":true,"resolved":true,"regressed":true}`
	for _, c := range []struct {
		name, actor, method, path, body string
		want                            int
	}{
		{"other org site", "viewer-user", http.MethodPut, foreign, body, http.StatusNotFound},
		{"other org user on this site", "carol", http.MethodPut, own, body, http.StatusNotFound},
		{"unknown site", "viewer-user", http.MethodPut, "/api/private/me/app-notifications/999999", body, http.StatusNotFound},
		{"bad site id", "viewer-user", http.MethodPut, "/api/private/me/app-notifications/x", body, http.StatusNotFound},
		{"missing field", "viewer-user", http.MethodPut, own, `{"assigned":true}`, http.StatusBadRequest},
		{"unknown field", "viewer-user", http.MethodPut, own, `{"assigned":true,"resolved":true,"regressed":true,"x":1}`, http.StatusBadRequest},
		{"api key", "key", http.MethodPut, own, body, http.StatusForbidden},
		{"api key list", "key", http.MethodGet, "/api/private/me/app-notifications", ``, http.StatusForbidden},
		{"anonymous", "", http.MethodPut, own, body, http.StatusUnauthorized},
	} {
		if res := f.request(t, c.actor, c.method, c.path, c.body); res.Code != c.want {
			t.Errorf("%s = %d %s, want %d", c.name, res.Code, res.Body, c.want)
		}
	}
	var n int
	_ = f.pool.QueryRow(context.Background(), `select count(*) from user_notification_prefs`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d rows saved by refused requests", n)
	}
	// Carol sees only her own organisation's projects.
	for _, row := range f.prefsFor(t, "carol") {
		if row.(map[string]any)["site_id"] != float64(f.foreignSite) {
			t.Fatalf("carol sees %v", row)
		}
	}
}
