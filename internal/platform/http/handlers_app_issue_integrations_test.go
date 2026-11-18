package platformhttp_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func (f issuesFixture) channel(t *testing.T, sameOrg bool) int64 {
	t.Helper()
	org := "(select org_id from sites where id=$1)"
	if !sameOrg {
		org = "(select id from orgs where id <> (select org_id from sites where id=$1) order by id limit 1)"
	}
	var id int64
	if err := f.pool.QueryRow(context.Background(), `insert into notification_channels(org_id,kind,name,enabled,config) values (`+org+`,'webhook','hook',true,'{}') returning id`, f.siteID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f issuesFixture) webhooksPath() string {
	return fmt.Sprintf("/api/private/sites/%d/app/issue-webhooks", f.siteID)
}

func TestAppIssueWebhookSettingsGuardRoleAndTenant(t *testing.T) {
	f := newAppIssuesFixture(t)
	own := f.channel(t, true)
	body := fmt.Sprintf(`{"channel_ids":[%d],"kinds":["created","resolved"]}`, own)
	if res := f.request(t, "viewer", http.MethodPut, f.webhooksPath(), body); res.Code != http.StatusForbidden {
		t.Errorf("viewer PUT = %d, want 403", res.Code)
	}
	if res := f.request(t, "key", http.MethodPut, f.webhooksPath(), body); res.Code != http.StatusForbidden {
		t.Errorf("api key PUT = %d, want 403", res.Code)
	}
	for _, m := range []string{http.MethodPut, http.MethodGet} {
		if res := f.request(t, "foreign-owner", m, f.webhooksPath(), body); res.Code != http.StatusNotFound {
			t.Errorf("foreign %s = %d, want 404", m, res.Code)
		}
	}
	out := f.json(t, "editor", http.MethodPut, f.webhooksPath(), body, http.StatusOK)
	if fmt.Sprint(out["kinds"]) != "[created resolved]" {
		t.Fatalf("put = %v", out)
	}
	got := f.json(t, "viewer", http.MethodGet, f.webhooksPath(), ``, http.StatusOK)
	if fmt.Sprint(got["channel_ids"]) != fmt.Sprintf("[%d]", own) {
		t.Fatalf("get = %v", got)
	}
	// Another organisation's channel and unknown kinds are refused.
	foreign := f.channel(t, false)
	for name, b := range map[string]string{
		"foreign channel": fmt.Sprintf(`{"channel_ids":[%d],"kinds":["created"]}`, foreign),
		"unknown kind":    fmt.Sprintf(`{"channel_ids":[%d],"kinds":["deleted"]}`, own),
	} {
		if res := f.request(t, "owner", http.MethodPut, f.webhooksPath(), b); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, res.Code, res.Body)
		}
	}
	var audits int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from audit_log where action='app.issue_webhooks_updated'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit rows = %d %v", audits, err)
	}
}

func TestAppFeedbackReadsGuardTenantAndHideTheHash(t *testing.T) {
	f := newAppIssuesFixture(t)
	if _, err := f.pool.Exec(context.Background(), `insert into app_feedback(site_id,issue_id,event_id,at,email_hash,message,url) values
($1,$2,'e1',now(),'\x0102','broken','/x'),($1,null,null,now()-interval '1 minute',null,'unlinked',null)`, f.siteID, f.crashIssue); err != nil {
		t.Fatal(err)
	}
	all := f.json(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/app/feedback", f.siteID), ``, http.StatusOK)
	if n := len(all["feedback"].([]any)); n != 2 {
		t.Fatalf("site feedback = %v", all)
	}
	own := f.json(t, "viewer", http.MethodGet, f.path("/feedback"), ``, http.StatusOK)
	rows := own["feedback"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["message"] != "broken" || rows[0].(map[string]any)["has_contact"] != true {
		t.Fatalf("issue feedback = %v", own)
	}
	res := f.request(t, "viewer", http.MethodGet, f.path("/feedback"), ``)
	if s := res.Body.String(); strings.Contains(s, "email") || strings.Contains(s, "hash") {
		t.Fatalf("response exposes contact data: %s", s)
	}
	for _, p := range []string{fmt.Sprintf("/api/private/sites/%d/app/feedback", f.siteID), f.path("/feedback")} {
		if res := f.request(t, "foreign-owner", http.MethodGet, p, ``); res.Code != http.StatusNotFound {
			t.Errorf("foreign GET %s = %d, want 404", p, res.Code)
		}
	}
	if res := f.request(t, "viewer", http.MethodGet, f.path("/feedback?limit=0"), ``); res.Code != http.StatusBadRequest {
		t.Errorf("limit=0 = %d", res.Code)
	}
}
