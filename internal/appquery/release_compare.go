package appquery

import (
	"context"
	"fmt"
	"math"
)

// ReleaseComparison puts two releases side by side. Delta is B minus A; a
// rate delta is nil when either side has no rate.
type ReleaseComparison struct {
	A     ReleaseDetail `json:"a"`
	B     ReleaseDetail `json:"b"`
	Delta struct {
		CrashFreeSessions *float64 `json:"crash_free_sessions"`
		CrashFreeUsers    *float64 `json:"crash_free_users"`
		NewIssues         int64    `json:"new_issues"`
	} `json:"delta"`
	// OnlyInB lists issues with events in B and none in A.
	OnlyInB []TopIssue `json:"only_in_b"`
}

func rateDelta(a, b *float64) *float64 {
	if a == nil || b == nil {
		return nil
	}
	v := *b - *a
	return &v
}

// CompareReleases reads both releases over the same range and filters.
func (q *Querier) CompareReleases(ctx context.Context, scope Scope, f Filter, a, b string) (ReleaseComparison, error) {
	var out ReleaseComparison
	var err error
	if out.A, err = q.ReleaseDetail(ctx, scope, f, a); err != nil {
		return ReleaseComparison{}, err
	}
	if out.B, err = q.ReleaseDetail(ctx, scope, f, b); err != nil {
		return ReleaseComparison{}, err
	}
	out.Delta.CrashFreeSessions = rateDelta(out.A.Health.CrashFreeSessions, out.B.Health.CrashFreeSessions)
	out.Delta.CrashFreeUsers = rateDelta(out.A.Health.CrashFreeUsers, out.B.Health.CrashFreeUsers)
	out.Delta.NewIssues = out.B.Health.NewIssues - out.A.Health.NewIssues
	w, err := q.window(scope, f)
	if err != nil {
		return ReleaseComparison{}, err
	}
	rows, err := q.pool.Query(ctx, `
select i.id, i.title, b.events from (
  select issue_id, sum(occurrences)::bigint as events from app_events
  where site_id = $1 and ts >= $2 and ts < $3 and release = $4 and ($5 = '' or environment = $5) and ($6 = '' or platform = $6)
    and issue_id is not null and kind <> 'session'
  group by issue_id
) b join app_issues i on i.id = b.issue_id
where i.merged_into is null
  and not exists (select 1 from app_events e where e.site_id = $1 and e.ts >= $2 and e.ts < $3 and e.release = $7 and e.issue_id = b.issue_id)
order by b.events desc, i.id limit $8`, scope.SiteID, w.start, w.end, b, f.Environment, f.Platform, a, maxReleaseIssues)
	if err != nil {
		return ReleaseComparison{}, fmt.Errorf("read issues only in release: %w", err)
	}
	defer rows.Close()
	out.OnlyInB = []TopIssue{}
	for rows.Next() {
		var t TopIssue
		if err := rows.Scan(&t.ID, &t.Title, &t.Events); err != nil {
			return ReleaseComparison{}, err
		}
		out.OnlyInB = append(out.OnlyInB, t)
	}
	return out, rows.Err()
}

// Regression search parameters.
const (
	defaultMinDrop        = 0.01
	regressionMinSessions = 100
)

// RegressionHit is the newest release whose crash-free sessions fell by at
// least the threshold against the release before it.
type RegressionHit struct {
	Release                   string     `json:"release"`
	Previous                  string     `json:"previous"`
	CrashFreeSessions         float64    `json:"crash_free_sessions"`
	PreviousCrashFreeSessions float64    `json:"previous_crash_free_sessions"`
	NewIssues                 []TopIssue `json:"new_issues"`
}

// FindRegressionRelease walks the releases newest first and returns the first
// whose crash-free sessions are at least minDrop (default 0.01) below its
// predecessor's. Releases with fewer than 100 sessions in the range are left
// out, so a tiny release neither triggers nor masks a regression. It returns
// nil when nothing regressed.
func (q *Querier) FindRegressionRelease(ctx context.Context, scope Scope, f Filter, minDrop float64) (*RegressionHit, error) {
	if minDrop <= 0 {
		minDrop = defaultMinDrop
	}
	f.Release = ""
	rows, err := q.Releases(ctx, scope, f)
	if err != nil {
		return nil, err
	}
	eligible := rows[:0:0]
	for _, r := range rows {
		if r.CrashFreeSessions != nil && r.Sessions >= regressionMinSessions {
			eligible = append(eligible, r)
		}
	}
	for i := 0; i+1 < len(eligible); i++ {
		cur, prev := eligible[i], eligible[i+1]
		if *prev.CrashFreeSessions-*cur.CrashFreeSessions < minDrop-1e-9 || math.IsNaN(minDrop) {
			continue
		}
		issues, err := q.releaseIssues(ctx, scope.SiteID, cur.Release, false, 20)
		if err != nil {
			return nil, err
		}
		return &RegressionHit{Release: cur.Release, Previous: prev.Release,
			CrashFreeSessions: *cur.CrashFreeSessions, PreviousCrashFreeSessions: *prev.CrashFreeSessions, NewIssues: issues}, nil
	}
	return nil, nil
}
