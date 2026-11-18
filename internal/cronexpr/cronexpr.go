// Package cronexpr parses 5-field cron expressions and computes the next
// matching time, with no dependencies. It is used by cron monitors to know when
// a check-in is due.
package cronexpr

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed cron expression.
type Schedule struct {
	minute, hour, dom, month, dow uint64 // bit sets
	domStar, dowStar              bool
}

type fieldSpec struct {
	name     string
	min, max int
	names    map[string]int
}

var (
	monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
	dowNames   = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}
	specs      = [5]fieldSpec{
		{"minute", 0, 59, nil}, {"hour", 0, 23, nil}, {"day-of-month", 1, 31, nil},
		{"month", 1, 12, monthNames}, {"day-of-week", 0, 7, dowNames},
	}
	macros = map[string]string{
		"@hourly": "0 * * * *", "@daily": "0 0 * * *", "@midnight": "0 0 * * *",
		"@weekly": "0 0 * * 0", "@monthly": "0 0 1 * *", "@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *",
	}
)

// Parse reads a 5-field expression (minute hour day-of-month month
// day-of-week) with *, lists, ranges, steps, month and weekday names, or one of
// @hourly @daily @weekly @monthly @yearly. Errors name the field (1-based).
func Parse(expr string) (Schedule, error) {
	expr = strings.TrimSpace(expr)
	if m, ok := macros[strings.ToLower(expr)]; ok {
		expr = m
	}
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return Schedule{}, fmt.Errorf("cron expression needs 5 fields, got %d", len(parts))
	}
	var sets [5]uint64
	for i, part := range parts {
		set, err := parseField(part, specs[i])
		if err != nil {
			return Schedule{}, fmt.Errorf("field %d (%s): %w", i+1, specs[i].name, err)
		}
		sets[i] = set
	}
	if sets[4]&(1<<7) != 0 { // 7 is Sunday too
		sets[4] |= 1
		sets[4] &^= 1 << 7
	}
	return Schedule{
		minute: sets[0], hour: sets[1], dom: sets[2], month: sets[3], dow: sets[4],
		domStar: strings.HasPrefix(parts[2], "*"), dowStar: strings.HasPrefix(parts[4], "*"),
	}, nil
}

func parseField(field string, spec fieldSpec) (uint64, error) {
	var set uint64
	for _, item := range strings.Split(field, ",") {
		rangePart, step, stepped := item, 1, false
		if before, after, ok := strings.Cut(item, "/"); ok {
			n, err := strconv.Atoi(after)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("invalid step %q", after)
			}
			rangePart, step, stepped = before, n, true
		}
		lo, hi := spec.min, spec.max
		switch {
		case rangePart == "*":
		case strings.Contains(rangePart, "-"):
			a, b, _ := strings.Cut(rangePart, "-")
			var err error
			if lo, err = parseValue(a, spec); err != nil {
				return 0, err
			}
			if hi, err = parseValue(b, spec); err != nil {
				return 0, err
			}
			if lo > hi {
				return 0, fmt.Errorf("range %q is backwards", rangePart)
			}
		default:
			v, err := parseValue(rangePart, spec)
			if err != nil {
				return 0, err
			}
			lo, hi = v, v
			if stepped {
				hi = spec.max // "5/15" runs from 5 to the end
			}
		}
		for v := lo; v <= hi; v += step {
			set |= 1 << uint(v)
		}
	}
	return set, nil
}

func parseValue(s string, spec fieldSpec) (int, error) {
	if v, ok := spec.names[strings.ToLower(s)]; ok {
		return v, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid value %q", s)
	}
	if v < spec.min || v > spec.max {
		return 0, fmt.Errorf("value %d is outside %d-%d", v, spec.min, spec.max)
	}
	return v, nil
}

func has(set uint64, v int) bool { return set&(1<<uint(v)) != 0 }

func (s Schedule) dayMatches(t time.Time) bool {
	domOK, dowOK := has(s.dom, t.Day()), has(s.dow, int(t.Weekday()))
	switch {
	case s.domStar && s.dowStar:
		return true
	case s.domStar:
		return dowOK
	case s.dowStar:
		return domOK
	default: // both restricted: classic cron ORs them
		return domOK || dowOK
	}
}

// Next returns the first matching minute strictly after the given time, in loc
// (UTC when nil). Wall-clock times that do not exist (a spring-forward gap) are
// skipped. The zero time is returned only when nothing matches within 8 years
// (an impossible date such as 31 February).
func (s Schedule) Next(after time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	t := after.In(loc).Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(8, 0, 0)
	for t.Before(limit) {
		if !has(s.month, int(t.Month())) {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
			continue
		}
		if !s.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
			continue
		}
		if !has(s.hour, t.Hour()) {
			t = t.Truncate(time.Hour).Add(time.Hour)
			continue
		}
		if !has(s.minute, t.Minute()) {
			t = t.Add(time.Minute)
			continue
		}
		if t.After(after) {
			return t.UTC()
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}

// NextInterval returns last plus n units (minute, hour, day, week, month,
// year). An unknown unit or n < 1 returns the zero time.
func NextInterval(last time.Time, n int, unit string) time.Time {
	if n < 1 {
		return time.Time{}
	}
	switch unit {
	case "minute":
		return last.Add(time.Duration(n) * time.Minute)
	case "hour":
		return last.Add(time.Duration(n) * time.Hour)
	case "day":
		return last.AddDate(0, 0, n)
	case "week":
		return last.AddDate(0, 0, 7*n)
	case "month":
		return last.AddDate(0, n, 0)
	case "year":
		return last.AddDate(n, 0, 0)
	}
	return time.Time{}
}
