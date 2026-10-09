package ingest

import (
	"testing"
	"time"

	"supermetry/internal/session"
)

type benchmarkStore struct{}

func (benchmarkStore) Touch(session.Hit) session.Result            { return session.Result{} }
func (benchmarkStore) Dirty(int) []session.Snapshot                { return nil }
func (benchmarkStore) ActiveSince(int32, time.Time) (int, []int32) { return 0, nil }
func (benchmarkStore) Restore([]session.Snapshot)                  {}
func (benchmarkStore) Expire(time.Time) int                        { return 0 }

func BenchmarkRecordAcceptedActivity(b *testing.B) {
	hit := session.Hit{SiteID: 1, TS: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), Kind: 0, PathID: 1, Country: "US", City: 2, Browser: 3, Source: 4}
	b.Run("without_recorder", func(b *testing.B) {
		store := benchmarkStore{}
		b.ReportAllocs()
		for b.Loop() {
			recordAcceptedActivity(store, hit, 5)
		}
	})
	b.Run("with_recorder", func(b *testing.B) {
		store := session.NewMemory(16, nil)
		b.ReportAllocs()
		for b.Loop() {
			recordAcceptedActivity(store, hit, 5)
		}
	})
}
