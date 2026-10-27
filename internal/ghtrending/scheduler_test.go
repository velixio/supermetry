package ghtrending

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/scheduler"
)

// The job registers with the real scheduler (advisory-lock coordination
// included), runs on start, and leaves an episode behind.
func TestJobRunsUnderTheRealScheduler(t *testing.T) {
	h := newHarness(t)
	id := h.addRepo(t, "a/a", "", true)
	h.f.answers = map[listKey][]result{
		{"", Daily}:  {ok(pageAt("a/a", 3))},
		{"", Weekly}: {ok(page("x/x"))},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.New(h.pool, nil, h.svc.Job()).Run(ctx) }()

	deadline := time.Now().Add(20 * time.Second)
	for {
		if eps := h.episodes(t, id); len(eps) == 1 && eps[0].LastRank == 3 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("the scheduler did not run the trending job")
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("scheduler: %v", err)
	}
	if got := h.rec.kinds(); len(got) != 1 || got[0] != "entered" {
		t.Fatalf("events = %v", got)
	}
}
