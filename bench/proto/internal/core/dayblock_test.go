package core

import (
	"reflect"
	"testing"
	"time"
)

func TestDecomposeDayRangeEdgeCases(t *testing.T) {
	t.Parallel()
	type wantBlock struct {
		start string
		level uint8
	}
	tests := []struct {
		name, zone string
		from, to   time.Time
		want       []wantBlock
		wantHours  map[string]time.Duration
	}{
		{
			name: "single day", zone: "Asia/Kolkata",
			from: time.Date(2026, 7, 1, 0, 0, 0, 0, time.FixedZone("IST", 5*3600+30*60)),
			to:   time.Date(2026, 7, 1, 23, 59, 0, 0, time.FixedZone("IST", 5*3600+30*60)),
			want: []wantBlock{{"2026-07-01", 0}},
		},
		{
			name: "month boundary", zone: "Asia/Kolkata",
			from: mustInstant(t, "2026-01-29T18:30:00Z"),
			to:   mustInstant(t, "2026-02-02T18:30:00Z"),
			want: []wantBlock{{"2026-01-30", 0}, {"2026-01-31", 2}},
		},
		{
			name: "leap day", zone: "Asia/Kathmandu",
			from: mustInstant(t, "2024-02-27T18:15:00Z"),
			to:   mustInstant(t, "2024-03-01T18:15:00Z"),
			want: []wantBlock{{"2024-02-28", 0}, {"2024-02-29", 1}, {"2024-03-02", 0}},
		},
		{
			name: "spring transition dates without DST in Kolkata", zone: "Asia/Kolkata",
			from:      mustInstant(t, "2024-03-08T18:30:00Z"),
			to:        mustInstant(t, "2024-03-11T18:30:00Z"),
			want:      []wantBlock{{"2024-03-09", 0}, {"2024-03-10", 1}, {"2024-03-12", 0}},
			wantHours: map[string]time.Duration{"2024-03-10": 24 * time.Hour},
		},
		{
			name: "spring transition dates without DST in Kathmandu", zone: "Asia/Kathmandu",
			from:      mustInstant(t, "2024-03-08T18:15:00Z"),
			to:        mustInstant(t, "2024-03-11T18:15:00Z"),
			want:      []wantBlock{{"2024-03-09", 0}, {"2024-03-10", 1}, {"2024-03-12", 0}},
			wantHours: map[string]time.Duration{"2024-03-10": 24 * time.Hour},
		},
		{
			name: "spring DST", zone: "America/New_York",
			from:      mustInstant(t, "2024-03-09T05:00:00Z"),
			to:        mustInstant(t, "2024-03-12T04:00:00Z"),
			want:      []wantBlock{{"2024-03-09", 0}, {"2024-03-10", 1}, {"2024-03-12", 0}},
			wantHours: map[string]time.Duration{"2024-03-10": 23 * time.Hour},
		},
		{
			name: "fall DST", zone: "America/New_York",
			from:      mustInstant(t, "2024-11-02T04:00:00Z"),
			to:        mustInstant(t, "2024-11-05T05:00:00Z"),
			want:      []wantBlock{{"2024-11-02", 0}, {"2024-11-03", 1}, {"2024-11-05", 0}},
			wantHours: map[string]time.Duration{"2024-11-03": 25 * time.Hour},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			loc, err := time.LoadLocation(tt.zone)
			if err != nil {
				t.Fatal(err)
			}
			from, to := DayOf(tt.from, loc), DayOf(tt.to, loc)
			got := DecomposeDayRange(from, to)
			var simple []wantBlock
			covered := from
			for _, b := range got {
				simple = append(simple, wantBlock{b.Start.String(), b.Level})
				if b.Level > MaxDayBlockLevel {
					t.Fatalf("block level %d exceeds max %d", b.Level, MaxDayBlockLevel)
				}
				if b.Start != covered {
					t.Fatalf("coverage starts at %s after %s", b.Start, covered)
				}
				if int64(b.Start)%int64(b.Days()) != 0 {
					t.Fatalf("block %+v is not epoch-aligned", b)
				}
				covered = b.End() + 1
			}
			if !reflect.DeepEqual(simple, tt.want) {
				t.Fatalf("DecomposeDayRange(%s, %s) = %#v, want %#v", from, to, simple, tt.want)
			}
			if covered != to+1 {
				t.Fatalf("coverage ends at %s, want %s", covered-1, to)
			}
			for day, want := range tt.wantHours {
				d, err := ParseDay(day)
				if err != nil {
					t.Fatal(err)
				}
				start, end := d.Bounds(loc)
				if got := end.Sub(start); got != want {
					t.Fatalf("%s length = %s, want %s", day, got, want)
				}
			}
		})
	}
}

func TestDecomposeDayRangeEmpty(t *testing.T) {
	if got := DecomposeDayRange(CivilDay(2026, 1, 2), CivilDay(2026, 1, 1)); got != nil {
		t.Fatalf("reversed range = %#v, want nil", got)
	}
}

func TestDayBlockSumsEqualDaily(t *testing.T) {
	t.Parallel()
	first := CivilDay(2025, 8, 1)
	daily := map[Day]map[string]Metrics{}
	for i := range 420 {
		d := first + Day(i)
		daily[d] = map[string]Metrics{
			"/": {Visitors: int64(i%17 + 1), Visits: int64(i%23 + 2), Pageviews: int64(i%29 + 3), Bounces: int64(i % 5), DurationMs: int64(i*101 + 7)},
			"x": {Visitors: int64(i%7 + 4), Visits: int64(i%11 + 5), Pageviews: int64(i%13 + 6), Bounces: int64(i % 3), DurationMs: int64(i*37 + 11)},
		}
	}
	blocks := map[DayBlock]map[string]Metrics{}
	for d := first; d < first+420; d++ {
		for level := uint8(0); level <= MaxDayBlockLevel; level++ {
			b := ContainingDayBlock(d, level)
			if b.Start < first || b.End() >= first+420 {
				continue
			}
			if blocks[b] != nil {
				continue
			}
			sum := map[string]Metrics{}
			for x := b.Start; x <= b.End(); x++ {
				for value, m := range daily[x] {
					total := sum[value]
					total.Add(m)
					sum[value] = total
				}
			}
			blocks[b] = sum
		}
	}
	for _, days := range []int{1, 28, 29, 350, 365, 420} {
		from, to := first, first+Day(days)-1
		got, want := map[string]Metrics{}, map[string]Metrics{}
		for _, b := range DecomposeDayRange(from, to) {
			for value, m := range blocks[b] {
				total := got[value]
				total.Add(m)
				got[value] = total
			}
		}
		for d := from; d <= to; d++ {
			for value, m := range daily[d] {
				total := want[value]
				total.Add(m)
				want[value] = total
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%dd block sum = %#v, want daily %#v", days, got, want)
		}
	}
}

func mustInstant(t *testing.T, value string) time.Time {
	t.Helper()
	x, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return x
}
