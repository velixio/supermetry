package ghstars

import (
	"context"
	"testing"
)

func TestReconcileStoresPrimaryLanguage(t *testing.T) {
	fake, server := newFakeGitHub(t, 1, [3]string{"1", "a", "2026-01-01T00:00:00Z"})
	svc, repo, _ := repoService(t, server)
	ctx := context.Background()
	language := func() string {
		var l string
		if err := svc.pool.QueryRow(ctx, `select language from github_star_repos where id=$1`, repo.ID).Scan(&l); err != nil {
			t.Fatal(err)
		}
		return l
	}
	if language() != "" {
		t.Fatal("language must start empty")
	}
	fake.repo["language"] = "Go"
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	if got := language(); got != "Go" {
		t.Fatalf("language = %q", got)
	}
	// GitHub reports null for repositories without a detected language.
	fake.repo["language"] = nil
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	if got := language(); got != "" {
		t.Fatalf("language = %q", got)
	}
}
