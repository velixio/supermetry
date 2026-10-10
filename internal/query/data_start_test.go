package query

import (
	"testing"
	"time"
)

func TestDataStartKeepsUnclosedRawDaysBeforeFirstClose(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC) }
	metadata := closeMetadata{siteStart: day(1), firstClosed: day(11)}
	if got := metadata.dataStart(day(1)); !got.Equal(day(1)) {
		t.Fatalf("dataStart(Jan 1) = %s, want Jan 1: raw days before the first close must stay queryable", got)
	}
	if got := metadata.dataStart(time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)); !got.Equal(day(1)) {
		t.Fatalf("dataStart before site creation = %s, want Jan 1", got)
	}
	imported := closeMetadata{siteStart: day(20), firstClosed: day(5)}
	if got := imported.dataStart(day(1)); !got.Equal(day(5)) {
		t.Fatalf("dataStart with closed data before creation = %s, want Jan 5", got)
	}
}
