package spanrollup

import (
	"testing"
	"time"
)

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestLocalHourBounds(t *testing.T) {
	h := func(s string) time.Time {
		v, err := time.Parse("2006-01-02T15:04", s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tests := []struct {
		name string
		zone string
		hour string
		want []string // start instants, each one hour long
	}{
		{"kathmandu midnight", "Asia/Kathmandu", "2026-11-18T00:00", []string{"2026-11-17T18:15"}},
		{"kolkata", "Asia/Kolkata", "2026-11-18T12:00", []string{"2026-11-18T06:30"}},
		{"new york fall-back", "America/New_York", "2026-11-01T01:00", []string{"2026-11-01T05:00", "2026-11-01T06:00"}},
		{"new york spring gap", "America/New_York", "2026-03-08T02:00", nil},
		{"new york spring after gap", "America/New_York", "2026-03-08T03:00", []string{"2026-03-08T07:00"}},
		{"utc", "UTC", "2026-11-18T13:00", []string{"2026-11-18T13:00"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := LocalHourBounds(h(tc.hour), zone(t, tc.zone))
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i, w := range tc.want {
				if !got[i][0].Equal(h(w)) || !got[i][1].Equal(h(w).Add(time.Hour)) {
					t.Fatalf("interval %d = %v, want start %s", i, got[i], w)
				}
			}
		})
	}
}
