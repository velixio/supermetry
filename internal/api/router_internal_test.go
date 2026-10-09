package api

import (
	"fmt"
	"testing"
	"time"
)

func TestAPITokenBucketsPruneIdleKeys(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	limiter := newAPITokenBuckets(2, 30, func() time.Time { return now })
	for i := 0; i < 255; i++ {
		limiter.take(fmt.Sprintf("key-%d", i))
	}
	now = now.Add(time.Minute)
	limiter.take("current")
	if got := len(limiter.buckets); got != 1 {
		t.Fatalf("bucket count = %d, want 1 after idle-key pruning", got)
	}
}

func TestAPITokenBucketsPruneEvictsOldestAtCapacity(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	limiter := newAPITokenBuckets(2, 30, func() time.Time { return now })
	for i := 0; i <= 10_000; i++ {
		limiter.take(fmt.Sprintf("key-%05d", i))
		now = now.Add(time.Nanosecond)
	}
	if got := len(limiter.buckets); got > 10_000 {
		t.Fatalf("bucket count = %d, want at most 10000", got)
	}
	if _, ok := limiter.buckets["key-00000"]; ok {
		t.Fatal("oldest bucket was not evicted")
	}
	if _, ok := limiter.buckets["key-10000"]; !ok {
		t.Fatal("newest bucket was evicted")
	}
}
