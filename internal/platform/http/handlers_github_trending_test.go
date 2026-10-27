package platformhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/ghstars"
	"supermetry/internal/ghtrending"
	"supermetry/internal/platform/admin"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

type trendingStatusBody struct {
	Enabled  bool              `json:"enabled"`
	Status   string            `json:"status"`
	Current  []json.RawMessage `json:"current"`
	Episodes []json.RawMessage `json:"episodes"`
	Lists    []json.RawMessage `json:"lists"`
}

func TestOrgGitHubTrending(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var org, other int64
	for slug, target := range map[string]*int64{"one": &org, "two": &other} {
		if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ($1,$1) returning id`, slug).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	users := map[string]int64{}
	for _, name := range []string{"owner", "admin", "editor", "viewer", "owner2"} {
		var id int64
		if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ($1,'U','hash') returning id`, name+"@example.test").Scan(&id); err != nil {
			t.Fatal(err)
		}
		users[name] = id
	}
	member := func(name string, role authz.Role, o int64) authz.Principal {
		return authz.Principal{UserID: users[name], OrgID: o, Role: role}
	}
	now := func() time.Time { return time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC) }
	data := &store.Store{Pool: pool}
	deps := platformhttp.Deps{
		Authenticator: actorAuthenticator{
			"owner":  member("owner", authz.RoleOwner, org),
			"admin":  member("admin", authz.RoleAdmin, org),
			"editor": member("editor", authz.RoleEditor, org),
			"viewer": member("viewer", authz.RoleViewer, org),
			"owner2": member("owner2", authz.RoleOwner, other),
		},
		Store: data, Admin: admin.New(data), Now: now,
	}
	box, err := platformcrypto.NewSecretBox(make([]byte, 32), "test/stars")
	if err != nil {
		t.Fatal(err)
	}
	stars := ghstars.NewService(pool, box, "https://stats.example.test", now)
	var repoID, otherRepoID int64
	for o, target := range map[int64]*int64{org: &repoID, other: &otherRepoID} {
		if err := pool.QueryRow(ctx, `insert into github_star_repos(org_id, public_id, repo, webhook_secret_enc) values ($1, $2, 'velixio/supermetry', '\x00') returning id`,
			o, "p"+jsonNumber(o)).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	id, otherID := jsonNumber(repoID), jsonNumber(otherRepoID)
	base := "/api/private/orgs/" + jsonNumber(org) + "/github-stars/repos/"
	otherBase := "/api/private/orgs/" + jsonNumber(other) + "/github-stars/repos/"

	off := platformhttp.New(deps) // star and trending services both left out
	if got := starsRequest(off, http.MethodGet, base+id+"/trending", "owner", ""); got.Code != http.StatusNotFound {
		t.Fatalf("unavailable status = %d", got.Code)
	}

	deps.GitHubStars = stars
	deps.GitHubTrending = ghtrending.NewService(pool, now)
	h := platformhttp.New(deps)

	var status trendingStatusBody
	get := func(actor, path string) *trendingStatusBody {
		t.Helper()
		got := starsRequest(h, http.MethodGet, path, actor, "")
		if got.Code != http.StatusOK {
			t.Fatalf("%s get %s = %d %s", actor, path, got.Code, got.Body)
		}
		status = trendingStatusBody{}
		if err := json.NewDecoder(strings.NewReader(got.Body.String())).Decode(&status); err != nil {
			t.Fatal(err)
		}
		if status.Current == nil || status.Episodes == nil || status.Lists == nil {
			t.Fatalf("arrays must be [] not null: %s", got.Body)
		}
		return &status
	}

	// Every member of the organization may read.
	for _, actor := range []string{"owner", "admin", "editor", "viewer"} {
		if s := get(actor, base+id+"/trending"); s.Enabled || s.Status != "off" {
			t.Fatalf("%s status = %+v", actor, s)
		}
	}
	// Other organizations' members, other organizations' repositories and unknown ids are not found.
	if got := starsRequest(h, http.MethodGet, base+id+"/trending", "owner2", ""); got.Code != http.StatusForbidden && got.Code != http.StatusNotFound {
		t.Fatalf("outsider status = %d", got.Code)
	}
	if got := starsRequest(h, http.MethodGet, base+otherID+"/trending", "owner", ""); got.Code != http.StatusNotFound {
		t.Fatalf("other org's repo status = %d", got.Code)
	}
	if got := starsRequest(h, http.MethodGet, base+"99999/trending", "owner", ""); got.Code != http.StatusNotFound {
		t.Fatalf("unknown repo status = %d", got.Code)
	}

	// Only owners and admins may turn it on, and only for their own organization's repository.
	for _, actor := range []string{"editor", "viewer"} {
		if got := starsRequest(h, http.MethodPatch, base+id, actor, `{"trending_enabled":true}`); got.Code != http.StatusForbidden {
			t.Fatalf("%s patch = %d", actor, got.Code)
		}
	}
	if got := starsRequest(h, http.MethodPatch, base+otherID, "owner", `{"trending_enabled":true}`); got.Code != http.StatusNotFound {
		t.Fatalf("cross-org patch = %d %s", got.Code, got.Body)
	}
	if got := starsRequest(h, http.MethodPatch, base+id, "admin", `{"trending_enabled":true}`); got.Code != http.StatusOK {
		t.Fatalf("admin patch = %d %s", got.Code, got.Body)
	}
	if s := get("viewer", base+id+"/trending"); !s.Enabled || s.Status != "waiting" {
		t.Fatalf("status = %+v", s)
	}
	// The other organization's row for the same GitHub repository is untouched.
	if s := get("owner2", otherBase+otherID+"/trending"); s.Enabled {
		t.Fatalf("other org status = %+v", s)
	}
	if got := starsRequest(h, http.MethodPatch, base+id, "owner", `{"trending_enabled":false}`); got.Code != http.StatusOK {
		t.Fatalf("owner patch off = %d %s", got.Code, got.Body)
	}
	if s := get("viewer", base+id+"/trending"); s.Enabled {
		t.Fatalf("still enabled: %+v", s)
	}
}
