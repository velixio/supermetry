package ghstars

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/testpg"
)

// newTestService returns a service on a fresh database whose GitHub API is a
// fake that knows every repository and has no stargazers.
func newTestService(t *testing.T) (*Service, *httptest.Server) {
	t.Helper()
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	box, err := platformcrypto.NewSecretBox(make([]byte, 32), "test/github-stars")
	if err != nil {
		t.Fatal(err)
	}
	_, server := newFakeGitHub(t, 0)
	svc := NewService(pool, box, "https://stats.example.test/", func() time.Time { return at("2026-10-10T12:00:00Z") })
	svc.APIBase = server.URL
	svc.HTTP = server.Client()
	return svc, server
}

func TestCreateNormalisesAndReturnsTheSecretOnce(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	repo, secret, err := svc.Create(ctx, CreateInput{Repo: " Velixio/SuperMetry ", Token: "tok", TimeZone: "Asia/Kathmandu"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Repo != "velixio/supermetry" || !repo.Enabled || !repo.HasToken || repo.TimeZone != "Asia/Kathmandu" || len(secret) != 64 {
		t.Fatalf("repo = %+v secret len %d", repo, len(secret))
	}
	if !strings.HasPrefix(repo.WebhookURL, "https://stats.example.test/api/github/webhook/") || len(repo.WebhookURL) != len("https://stats.example.test/api/github/webhook/")+16 {
		t.Fatalf("webhook url = %q", repo.WebhookURL)
	}
	// Neither the stored row nor any later read exposes the secret or the token in plaintext.
	var rawSecret, rawToken []byte
	if err := svc.pool.QueryRow(ctx, `select webhook_secret_enc, token_enc from github_star_repos where id=$1`, repo.ID).Scan(&rawSecret, &rawToken); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawSecret), secret) || strings.Contains(string(rawToken), "tok") {
		t.Fatal("secret or token stored in plaintext")
	}
	if plain, err := svc.open(rawSecret); err != nil || plain != secret {
		t.Fatalf("secret does not round-trip: %q %v", plain, err)
	}
	list, _ := svc.List(ctx)
	if len(list) != 1 || list[0].ID != repo.ID {
		t.Fatalf("list = %+v", list)
	}
}

func TestCreateWithoutBaseURLReturnsAPath(t *testing.T) {
	svc, _ := newTestService(t)
	svc.BaseURL = ""
	repo, _, err := svc.Create(context.Background(), CreateInput{Repo: "a/b"}, 0)
	if err != nil || !strings.HasPrefix(repo.WebhookURL, "/api/github/webhook/") || repo.TimeZone != "UTC" {
		t.Fatalf("repo = %+v, %v", repo, err)
	}
}

func TestCreateValidation(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	for name, in := range map[string]CreateInput{
		"shape":   {Repo: "justname"},
		"slashes": {Repo: "a/b/c"},
		"spaces":  {Repo: "a b/c"},
		"zone":    {Repo: "a/b", TimeZone: "Mars/Base"},
		"local":   {Repo: "a/b", TimeZone: "Local"},
	} {
		if _, _, err := svc.Create(ctx, in, 0); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, _, err := svc.Create(ctx, CreateInput{Repo: "a/b"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Create(ctx, CreateInput{Repo: "A/B"}, 0); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate err = %v", err)
	}
}

func TestCreateChecksTheRepositoryOnGitHub(t *testing.T) {
	cases := map[int]error{http.StatusNotFound: ErrInvalid, http.StatusUnauthorized: ErrInvalid}
	for status, want := range cases {
		svc, _ := newTestService(t)
		down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		t.Cleanup(down.Close)
		svc.APIBase, svc.HTTP = down.URL, down.Client()
		if _, _, err := svc.Create(context.Background(), CreateInput{Repo: "a/b"}, 0); !errors.Is(err, want) {
			t.Errorf("status %d: err = %v", status, err)
		}
		if list, _ := svc.List(context.Background()); len(list) != 0 {
			t.Errorf("status %d: repository saved anyway", status)
		}
	}
	// Rate limits, outages and unreachable hosts do not block the save.
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusBadGateway} {
		svc, _ := newTestService(t)
		down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		t.Cleanup(down.Close)
		svc.APIBase, svc.HTTP = down.URL, down.Client()
		if _, _, err := svc.Create(context.Background(), CreateInput{Repo: "a/b"}, 0); err != nil {
			t.Errorf("status %d: err = %v", status, err)
		}
	}
	svc, _ := newTestService(t)
	dead := httptest.NewServer(http.NotFoundHandler())
	svc.APIBase, svc.HTTP = dead.URL, dead.Client()
	dead.Close()
	if _, _, err := svc.Create(context.Background(), CreateInput{Repo: "a/b"}, 0); err != nil {
		t.Errorf("unreachable: err = %v", err)
	}
}

func TestCreateEnforcesTheRepositoryCap(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	for i := 0; i < MaxRepos; i++ {
		name := "o/r" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		if _, _, err := svc.Create(ctx, CreateInput{Repo: name}, 0); err != nil {
			t.Fatalf("repo %d: %v", i, err)
		}
	}
	if _, _, err := svc.Create(ctx, CreateInput{Repo: "o/one-too-many"}, 0); !errors.Is(err, ErrLimit) {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdateRotateAndAudit(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	repo, secret, _ := svc.Create(ctx, CreateInput{Repo: "a/b", Token: "t1"}, 0)
	off, zone := false, "Asia/Kolkata"
	got, err := svc.Update(ctx, repo.ID, UpdateInput{Enabled: &off, TimeZone: &zone}, 0)
	if err != nil || got.Enabled || got.TimeZone != zone || !got.HasToken {
		t.Fatalf("update = %+v, %v", got, err)
	}
	got, err = svc.Update(ctx, repo.ID, UpdateInput{ClearToken: true}, 0)
	if err != nil || got.HasToken {
		t.Fatalf("clear token = %+v, %v", got, err)
	}
	newToken := "t2"
	if got, _ = svc.Update(ctx, repo.ID, UpdateInput{Token: &newToken}, 0); !got.HasToken {
		t.Fatal("token not set")
	}
	bad := "Mars/Base"
	if _, err := svc.Update(ctx, repo.ID, UpdateInput{TimeZone: &bad}, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad zone err = %v", err)
	}
	if _, err := svc.Update(ctx, 99999, UpdateInput{}, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown err = %v", err)
	}
	rotated, fresh, err := svc.RotateSecret(ctx, repo.ID, 0)
	if err != nil || fresh == secret || len(fresh) != 64 || rotated.WebhookURL != repo.WebhookURL {
		t.Fatalf("rotate = %+v %q %v", rotated, fresh, err)
	}
	var audits int
	if err := svc.pool.QueryRow(ctx, `select count(*) from audit_log where target_type='github_star_repo' and target_id=$1 and metadata::text not like '%'||$2||'%' and metadata::text not like '%t2%'`, repo.ID, fresh).Scan(&audits); err != nil || audits != 5 {
		t.Fatalf("audit rows = %d, %v", audits, err)
	}
}

func TestDeleteDisablesOrRemovesWithData(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	repo, _, _ := svc.Create(ctx, CreateInput{Repo: "a/b"}, 0)
	other, _, _ := svc.Create(ctx, CreateInput{Repo: "a/c"}, 0)
	for _, r := range []Repo{repo, other} {
		store, _, _ := svc.StoreFor(ctx, r.ID)
		if err := store.InsertEvents(ctx, []Event{{UserID: 1, Login: "u", Action: ActionStarred, OccurredAt: at("2026-10-01T00:00:00Z"), Source: SourceWebhook}}); err != nil {
			t.Fatal(err)
		}
		_ = store.SaveState(ctx, Counts{Stars: 1}, at("2026-10-01T00:00:00Z"))
		_ = store.SaveSnapshot(ctx, at("2026-10-01T00:00:00Z"), Counts{Stars: 1})
		_, _ = store.AddAnnotation(ctx, at("2026-10-01T00:00:00Z"), "x", 0)
		_, _ = svc.pool.Exec(ctx, `insert into github_star_erasures(repo,user_id) values ($1,9)`, r.Repo)
	}
	removed, err := svc.Delete(ctx, repo.ID, false, 0)
	if err != nil || removed {
		t.Fatalf("disable = %v, %v", removed, err)
	}
	if got, _ := svc.Get(ctx, repo.ID); got.Enabled {
		t.Fatal("repository still enabled")
	}
	count := func(table, name string) int {
		var n int
		if err := svc.pool.QueryRow(ctx, `select count(*) from `+table+` where repo=$1`, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count("github_star_events", "a/b") != 1 {
		t.Fatal("disable deleted data")
	}
	removed, err = svc.Delete(ctx, repo.ID, true, 0)
	if err != nil || !removed {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	for _, table := range []string{"github_star_events", "github_star_state", "github_star_snapshots", "github_star_annotations", "github_star_erasures"} {
		if count(table, "a/b") != 0 || count(table, "a/c") != 1 {
			t.Errorf("%s: removed repo has %d rows, other has %d", table, count(table, "a/b"), count(table, "a/c"))
		}
	}
	if _, err := svc.Get(ctx, repo.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after remove err = %v", err)
	}
	if _, err := svc.Delete(ctx, repo.ID, true, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove err = %v", err)
	}
}
