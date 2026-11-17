package appissues

import (
	"context"
	"fmt"
)

// ImpactDays is the window Impact looks back over.
const ImpactDays = 30

// CountBy is a value with the number of events that carried it.
type CountBy struct {
	Value  string `json:"value"`
	Events int64  `json:"events"`
}

// Impact says how far an issue reaches over the last 30 days: counts only,
// never the hashes behind them.
type Impact struct {
	Days      int       `json:"days"`
	Events    int64     `json:"events"`
	Users     int64     `json:"users"`
	Sessions  int64     `json:"sessions"`
	Releases  []CountBy `json:"releases"`
	Platforms []CountBy `json:"platforms"`
}

const impactTop = 20

// Impact counts the issue's events, distinct users and sessions, and the
// releases and platforms they came from, over the 30 days before now.
func (s *Service) Impact(ctx context.Context, siteID int32, issueID int64) (Impact, error) {
	if err := s.issueExists(ctx, siteID, issueID); err != nil {
		return Impact{}, err
	}
	since := s.now().AddDate(0, 0, -ImpactDays)
	out := Impact{Days: ImpactDays, Releases: []CountBy{}, Platforms: []CountBy{}}
	if err := s.pool.QueryRow(ctx, `select coalesce(sum(occurrences), 0), count(distinct user_hash), count(distinct session_hash)
from app_events where site_id = $1 and issue_id = $2 and ts >= $3`, siteID, issueID, since).Scan(&out.Events, &out.Users, &out.Sessions); err != nil {
		return Impact{}, fmt.Errorf("read issue impact: %w", err)
	}
	for _, dim := range []struct {
		col string
		dst *[]CountBy
	}{{"release", &out.Releases}, {"platform", &out.Platforms}} {
		rows, err := s.pool.Query(ctx, `select `+dim.col+`, sum(occurrences)::bigint from app_events
where site_id = $1 and issue_id = $2 and ts >= $3 and `+dim.col+` <> '' group by 1 order by 2 desc, 1 limit $4`, siteID, issueID, since, impactTop)
		if err != nil {
			return Impact{}, fmt.Errorf("read issue impact: %w", err)
		}
		for rows.Next() {
			var c CountBy
			if err := rows.Scan(&c.Value, &c.Events); err != nil {
				rows.Close()
				return Impact{}, err
			}
			*dim.dst = append(*dim.dst, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return Impact{}, err
		}
	}
	return out, nil
}

