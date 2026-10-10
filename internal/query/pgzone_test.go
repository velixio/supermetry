package query

import (
	"context"
	"supermetry/internal/testpg"
	"testing"
	"time"
)

func testResolver(known ...string) *zoneResolver {
	return &zoneResolver{load: func(context.Context) ([]string, error) { return known, nil }}
}

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func TestResolverMapsAnAliasTheDatabaseDoesNotKnow(t *testing.T) {
	resolver := testResolver("UTC", "Asia/Tokyo", "Asia/Kolkata", "Europe/London", "America/New_York")
	if got := resolver.resolve(context.Background(), mustLoad(t, "Asia/Calcutta")); got != "Asia/Kolkata" {
		t.Fatalf("Asia/Calcutta resolved to %q, want Asia/Kolkata", got)
	}
}

func TestResolverKeepsKnownNamesAndUTC(t *testing.T) {
	resolver := testResolver("UTC", "Asia/Kolkata", "Europe/London")
	for name, want := range map[string]string{"Europe/London": "Europe/London", "UTC": "UTC"} {
		if got := resolver.resolve(context.Background(), mustLoad(t, name)); got != want {
			t.Fatalf("%s resolved to %q, want %q", name, got, want)
		}
	}
}

func TestResolverDoesNotPickAZoneWithDifferentOffsets(t *testing.T) {
	resolver := testResolver("UTC", "Asia/Tokyo", "Europe/London")
	if got := resolver.resolve(context.Background(), mustLoad(t, "Asia/Calcutta")); got != "Asia/Calcutta" {
		t.Fatalf("resolved to %q, want the original name when nothing matches", got)
	}
}

func TestResolverRespectsDaylightSavingRules(t *testing.T) {
	resolver := testResolver("UTC", "Europe/London", "Africa/Abidjan")
	if got := resolver.resolve(context.Background(), mustLoad(t, "Europe/Belfast")); got != "Europe/London" {
		t.Fatalf("Europe/Belfast resolved to %q, want Europe/London", got)
	}
}

func TestPostgresZoneIsAcceptedByTheDatabase(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	for _, name := range []string{"Asia/Calcutta", "Asia/Katmandu", "Asia/Kolkata", "America/Buenos_Aires", "Europe/London", "UTC"} {
		location := mustLoad(t, name)
		zone := postgresZone(ctx, pool, location)
		var shifted time.Time
		if err := pool.QueryRow(ctx, `select (timestamptz '2026-03-01 12:00:00+00' at time zone $1)`, zone).Scan(&shifted); err != nil {
			t.Fatalf("%s resolved to %q, which the database rejects: %v", name, zone, err)
		}
		_, wantOffset := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC).In(location).Zone()
		if got := shifted.Sub(time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)); got != time.Duration(wantOffset)*time.Second {
			t.Fatalf("%s via %q shifted by %s, want %ds", name, zone, got, wantOffset)
		}
	}
}
