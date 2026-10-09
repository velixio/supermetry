package storage_test

import (
	"testing"

	"supermetry/internal/testenv"
)

func TestEngagementStorageCost(t *testing.T) {
	env := testenv.Start(t)
	testenv.ReplayTiny(t, env)
	env.CloseAll(t)

	withEngagement := env.EventsBytes(t, true)
	withoutEngagement := env.EventsBytes(t, false)
	if withoutEngagement == 0 {
		t.Fatal("non-engagement events occupy zero bytes")
	}
	ratio := float64(withEngagement) / float64(withoutEngagement)
	t.Logf("engagement adds %.0f%% to hot events storage", ratio*100)
	if ratio > 0.6 {
		t.Fatalf("engagement rows add %.0f%% (>60%%): switch to folding engagement into session upserts (spec §3.5 fallback)", ratio*100)
	}
}
