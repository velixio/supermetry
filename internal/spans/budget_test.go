package spans

import (
	"testing"
	"time"
)

func TestBudgetTake(t *testing.T) {
	b := NewBudget(60_000)
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	if got := b.Take(1, 50_000, now); got != 50_000 {
		t.Fatalf("first take %d", got)
	}
	if got := b.Take(1, 20_000, now); got != 10_000 {
		t.Fatalf("second take %d", got)
	}
	if got := b.Take(2, 100, now); got != 100 {
		t.Fatalf("other site %d", got)
	}
	if got := b.Take(1, 5000, now.Add(time.Second)); got != 1000 {
		t.Fatalf("refill take %d, want 1000", got)
	}
}

func TestBudgetUnlimited(t *testing.T) {
	if got := NewBudget(0).Take(1, 1<<20, time.Now()); got != 1<<20 {
		t.Fatalf("got %d", got)
	}
}
