package ingest

import (
	"fmt"
	"testing"
)

func TestTokenBucketsEvictsOldestAtCapacity(t *testing.T) {
	limiter := NewTokenBuckets(0, 1).(*tokenBuckets)
	for i := 0; i <= maxTokenBuckets; i++ {
		limiter.Allow(fmt.Sprintf("key-%05d", i))
	}

	if got := len(limiter.buckets); got > maxTokenBuckets {
		t.Fatalf("bucket count=%d, want at most %d", got, maxTokenBuckets)
	}
	if _, ok := limiter.buckets["key-00000"]; ok {
		t.Fatal("oldest bucket was not evicted")
	}
	if _, ok := limiter.buckets[fmt.Sprintf("key-%05d", maxTokenBuckets)]; !ok {
		t.Fatal("newest bucket was evicted")
	}
}
