package sites

import (
	"testing"
	"time"
)

func TestStartDayUsesTheSiteTimeZone(t *testing.T) {
	createdAt := time.Date(2026, 1, 10, 22, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name, zone string
		want       time.Time
	}{
		{"alias unknown to some databases", "Asia/Calcutta", time.Date(2026, 1, 11, 0, 0, 0, 0, time.UTC)},
		{"canonical name", "Asia/Kolkata", time.Date(2026, 1, 11, 0, 0, 0, 0, time.UTC)},
		{"zone behind UTC", "America/Los_Angeles", time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)},
		{"UTC", "UTC", time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)},
		{"unknown name falls back to UTC", "Not/AZone", time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)},
		{"empty name", "", time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := StartDay(createdAt, tt.zone); !got.Equal(tt.want) {
				t.Fatalf("StartDay(%s, %q) = %s, want %s", createdAt, tt.zone, got, tt.want)
			}
		})
	}
}
