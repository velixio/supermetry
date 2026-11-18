package spanrollup

import (
	"sort"
	"time"
)

// Wall returns t's wall-clock hour as a UTC time (minutes and below dropped):
// the representation of a site-local hour in the timestamp columns.
func Wall(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, time.UTC)
}

// LocalHourBounds returns the UTC intervals whose wall-clock hour in loc equals
// hour's wall clock: one normally, two for the repeated hour of a DST
// fall-back, none for the hour skipped by a spring-forward. Bounds are
// computed here so Postgres needs no time-zone data. Whole-hour offsets and
// +05:30 / +05:45 style offsets are all handled because each candidate starts
// exactly at wall minute 00.
func LocalHourBounds(hour time.Time, loc *time.Location) [][2]time.Time {
	if loc == nil {
		loc = time.UTC
	}
	wall := Wall(hour)
	offsets := map[int]struct{}{}
	for _, d := range []time.Duration{-24 * time.Hour, -12 * time.Hour, 0, 12 * time.Hour, 24 * time.Hour} {
		_, off := wall.Add(d).In(loc).Zone()
		offsets[off] = struct{}{}
	}
	var out [][2]time.Time
	seen := map[time.Time]struct{}{}
	for off := range offsets {
		start := wall.Add(-time.Duration(off) * time.Second)
		if _, dup := seen[start]; dup {
			continue
		}
		l := start.In(loc)
		if l.Year() == wall.Year() && l.Month() == wall.Month() && l.Day() == wall.Day() && l.Hour() == wall.Hour() && l.Minute() == 0 {
			seen[start] = struct{}{}
			out = append(out, [2]time.Time{start, start.Add(time.Hour)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0].Before(out[j][0]) })
	return out
}

// hourEnd returns the instant the local hour is over (the latest interval
// end), and false for a skipped hour.
func hourEnd(hour time.Time, loc *time.Location) (time.Time, bool) {
	b := LocalHourBounds(hour, loc)
	if len(b) == 0 {
		return time.Time{}, false
	}
	return b[len(b)-1][1], true
}
