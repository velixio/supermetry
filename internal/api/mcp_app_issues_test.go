package api_test

import (
	"context"
	"strings"
	"testing"

	"supermetry/internal/testapi"
)

func TestMCPIssueToolsAreScrubbedAndKeepBoundaries(t *testing.T) {
	env := testapi.New(t)
	org, otherOrg := env.Org(t), env.Org(t)
	site, other, foreign := env.AppSite(t, org), env.AppSite(t, org), env.AppSite(t, otherOrg)
	seed := seedMCPAppSite(t, env, site, "Timeline issue")
	otherSeed := seedMCPAppSite(t, env, other, "Other site issue")
	ctx := context.Background()
	// A user whose display name is an address, a plain-named user, an address
	// inside activity data, and end-user hashes on the events.
	var emailUser, namedUser int64
	if err := env.Pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('leaky@example.test','leaky@example.test','h') returning id`).Scan(&emailUser); err != nil {
		t.Fatal(err)
	}
	if err := env.Pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('dana@example.test','Dana','h') returning id`).Scan(&namedUser); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Pool.Exec(ctx, `insert into app_issue_activity(site_id,issue_id,at,actor_user_id,kind,data) values
($1,$2,'2026-10-11T11:00:00Z',$3,'priority','{"from":"medium","to":"high","note":"ping bob@corp.example"}'),
($1,$2,'2026-10-11T12:00:00Z',$4,'status','{"from":"open","to":"resolved"}'),
($1,$2,'2026-10-11T10:00:00Z',null,'first_seen','{}')`, site.ID, seed.issue, emailUser, namedUser); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Pool.Exec(ctx, `update app_events set user_hash = decode('abcdef0123456789abcdef0123456789','hex') where issue_id = $1`, seed.issue); err != nil {
		t.Fatal(err)
	}
	key := env.APIKey(t, org, "stats:read")
	restricted := env.APIKeyForSites(t, org, []int32{site.ID}, "stats:read")
	noScope := env.APIKey(t, org, "data:export")
	args := func(s interface{ String() string }, issue int64, extra string) string {
		return `{"site":` + s.String() + `,"issue":` + itoa(issue) + extra + `}`
	}

	for _, tool := range []string{"get_issue_timeline", "get_issue_impact"} {
		r := mcpCall(t, env, key, tool, args(site, seed.issue, ``))
		body := string(r.Body)
		if strings.Contains(body, `"isError":true`) {
			t.Fatalf("%s failed: %s", tool, body)
		}
		for _, banned := range []string{"@", "leaky", "corp.example", "user_hash", "abcdef0123", "install_hash", "session_hash", seed.rawID, "deadbeef", key} {
			if strings.Contains(strings.ToLower(body), strings.ToLower(banned)) {
				t.Fatalf("%s output contains %q: %s", tool, banned, body)
			}
		}
		// Another site's issue, another organisation's site and a key limited to other sites.
		if r := mcpCall(t, env, key, tool, args(site, otherSeed.issue, ``)); !strings.Contains(string(r.Body), "issue not found") {
			t.Fatalf("%s cross-site issue: %s", tool, r.Body)
		}
		if r := mcpCall(t, env, key, tool, args(foreign, seed.issue, ``)); !strings.Contains(string(r.Body), "site not found") {
			t.Fatalf("%s foreign org: %s", tool, r.Body)
		}
		if r := mcpCall(t, env, restricted, tool, args(other, otherSeed.issue, ``)); !strings.Contains(string(r.Body), "site not found") {
			t.Fatalf("%s restricted key: %s", tool, r.Body)
		}
		if r := mcpCall(t, env, noScope, tool, args(site, seed.issue, ``)); !strings.Contains(string(r.Body), "-32003") {
			t.Fatalf("%s without scope: %s", tool, r.Body)
		}
	}
	tl := string(mcpCall(t, env, key, "get_issue_timeline", args(site, seed.issue, `,"limit":2`)).Body)
	if !strings.Contains(tl, `\"name\":\"Dana\"`) || !strings.Contains(tl, "[email]") || strings.Count(tl, `\"kind\":`) < 2 {
		t.Fatalf("timeline = %s", tl)
	}
	if strings.Contains(tl, "first_seen") {
		t.Fatalf("limit 2 returned the third row: %s", tl)
	}
	if r := mcpCall(t, env, key, "get_issue_timeline", args(site, seed.issue, `,"limit":101`)); !strings.Contains(string(r.Body), "limit must be between 1 and 100") {
		t.Fatalf("limit 101 accepted: %s", r.Body)
	}
	im := string(mcpCall(t, env, key, "get_issue_impact", args(site, seed.issue, ``)).Body)
	if !strings.Contains(im, `\"users\":1`) || !strings.Contains(im, `\"platforms\"`) {
		t.Fatalf("impact = %s", im)
	}
}
