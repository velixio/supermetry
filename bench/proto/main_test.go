package main

import (
	"testing"
	"time"

	"supermetry/bench/proto/internal/core"
)

func TestRetentionCutoffKeepsRawDaysIncludingAsOf(t *testing.T) {
	asOf := time.Date(2026, 7, 21, 17, 0, 0, 0, time.FixedZone("x", 5*3600+30*60))
	got, err := retentionCutoff(asOf, 7)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("cutoff = %s, want %s", got, want)
	}
}

func TestRestoreSessionsInstallsEveryPersistedSession(t *testing.T) {
	c := core.NewCache(core.CacheConfig{})
	sessions := []core.Session{
		core.RestoreSession(core.SessionState{SiteID: 1, VisitorID: 1, Day: 1, Start: 10, Last: 20}),
		core.RestoreSession(core.SessionState{SiteID: 1, VisitorID: 2, Day: 1, Start: 10, Last: 20}),
	}
	if got := restoreSessions(c, sessions); got != 2 || c.Len() != 2 {
		t.Fatalf("restored=%d len=%d", got, c.Len())
	}
}

func TestRetentionCutoffRejectsNonPositiveDays(t *testing.T) {
	if _, err := retentionCutoff(time.Now(), 0); err == nil {
		t.Fatal("expected error")
	}
}
