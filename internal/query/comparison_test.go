package query

import (
	"testing"
	"time"

	"supermetry/internal/api"
)

func TestComparisonRange(t *testing.T) {
	tests := []struct {
		name     string
		compare  api.QueryRequestCompare
		from, to string
		wantFrom string
		wantTo   string
	}{
		{name: "previous period", compare: api.PreviousPeriod, from: "2026-10-01", to: "2026-10-07", wantFrom: "2026-09-24", wantTo: "2026-09-30"},
		{name: "previous year", compare: api.PreviousYear, from: "2024-02-29", to: "2024-03-01", wantFrom: "2023-02-28", wantTo: "2023-03-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from, to := comparisonRange(mustComparisonDay(tt.from), mustComparisonDay(tt.to), tt.compare)
			if from.Format(time.DateOnly) != tt.wantFrom || to.Format(time.DateOnly) != tt.wantTo {
				t.Fatalf("range=%s..%s, want %s..%s", from.Format(time.DateOnly), to.Format(time.DateOnly), tt.wantFrom, tt.wantTo)
			}
		})
	}
}

func mustComparisonDay(value string) time.Time {
	day, err := time.Parse(time.DateOnly, value)
	if err != nil {
		panic(err)
	}
	return day
}
