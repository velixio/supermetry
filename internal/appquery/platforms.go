package appquery

import (
	"context"
	"fmt"
	"sort"
)

// PlatformRow is one platform's health over the range.
type PlatformRow struct {
	Platform          string   `json:"platform"`
	Events            int64    `json:"events"`
	Crashes           int64    `json:"crashes"`
	CrashFreeUsers    *float64 `json:"crash_free_users"`
	CrashFreeSessions *float64 `json:"crash_free_sessions"`
	// Symbolicated counts error events whose frames were resolved; Missing
	// counts those left unresolved or only partly resolved because a debug
	// file or mapping was absent.
	Symbolicated int64 `json:"symbolicated"`
	Missing      int64 `json:"missing"`
}

// PlatformBreakdown returns one row per platform that has events in the range,
// busiest first. Health uses the same raw-event definitions as the overview
// (so it follows the release and environment filters); the symbolication
// counts are event rows, not occurrences, and ignore session pings. A platform
// filter narrows it to that one platform.
func (q *Querier) PlatformBreakdown(ctx context.Context, scope Scope, f Filter) ([]PlatformRow, error) {
	w, err := q.window(scope, f)
	if err != nil {
		return nil, err
	}
	health, err := q.healthBy(ctx, w, "", "platform")
	if err != nil {
		return nil, err
	}
	sym := map[string][2]int64{}
	rows, err := q.pool.Query(ctx, `
select platform, count(*) filter (where symbolication = 'ok'), count(*) filter (where symbolication in ('missing', 'partial'))
from app_events where `+eventWhere+` and kind <> 'session'
group by platform`, w.evArgs()...)
	if err != nil {
		return nil, fmt.Errorf("read symbolication by platform: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var ok, missing int64
		if err := rows.Scan(&p, &ok, &missing); err != nil {
			return nil, err
		}
		sym[p] = [2]int64{ok, missing}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]PlatformRow, 0, len(health))
	for _, h := range health {
		p := h.keys[0]
		out = append(out, PlatformRow{
			Platform: p, Events: h.Events, Crashes: h.Crashes,
			CrashFreeUsers:    h.crashFreeUsers(),
			CrashFreeSessions: h.crashFreeSessions(),
			Symbolicated:      sym[p][0], Missing: sym[p][1],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Events != out[j].Events {
			return out[i].Events > out[j].Events
		}
		return out[i].Platform < out[j].Platform
	})
	return out, nil
}
