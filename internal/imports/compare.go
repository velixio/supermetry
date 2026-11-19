package imports

import (
	"context"
	"math"
	"sort"
	"time"
)

// DayTotals are one civil day's headline numbers from one source.
type DayTotals struct {
	Day       time.Time
	Visitors  int64
	Visits    int64
	Pageviews int64
}

// SideTotals is one side of a comparison.
type SideTotals struct {
	Visitors  int64 `json:"visitors"`
	Visits    int64 `json:"visits"`
	Pageviews int64 `json:"pageviews"`
}

// DayComparison sets an imported day against what Supermetry collected itself.
// Native is nil when nothing was collected that day (which is not the same as
// zero). Differences are (native - imported) / imported in percent, nil when
// the imported figure is zero or there is no native side.
type DayComparison struct {
	Day              string      `json:"day"`
	Imported         SideTotals  `json:"imported"`
	Native           *SideTotals `json:"native"`
	VisitorsDiffPct  *float64    `json:"visitors_diff_pct"`
	PageviewsDiffPct *float64    `json:"pageviews_diff_pct"`
}

func diffPct(imported, native int64) *float64 {
	if imported <= 0 {
		return nil
	}
	value := math.Round((float64(native-imported)/float64(imported)*100)*10) / 10
	return &value
}

// Compare returns one entry per imported day, oldest first. Native days with
// no imported counterpart are not reported.
func Compare(imported, native []DayTotals) []DayComparison {
	byDay := make(map[time.Time]DayTotals, len(native))
	for _, day := range native {
		byDay[day.Day] = day
	}
	result := make([]DayComparison, 0, len(imported))
	for _, day := range imported {
		entry := DayComparison{
			Day:      day.Day.Format(time.DateOnly),
			Imported: SideTotals{Visitors: day.Visitors, Visits: day.Visits, Pageviews: day.Pageviews},
		}
		if other, ok := byDay[day.Day]; ok {
			entry.Native = &SideTotals{Visitors: other.Visitors, Visits: other.Visits, Pageviews: other.Pageviews}
			entry.VisitorsDiffPct = diffPct(day.Visitors, other.Visitors)
			entry.PageviewsDiffPct = diffPct(day.Pageviews, other.Pageviews)
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Day < result[j].Day })
	return result
}

// ImportedDaily returns the imported daily totals in [from, to]. With a zero
// importID the newest completed import wins per day (the rule queries use);
// otherwise only that import's days are returned. Stored days are returned even
// when collected data hides them from dashboards, which is what a
// reconciliation needs.
func (s *Store) ImportedDaily(ctx context.Context, siteID int32, from, to time.Time, importID int64) ([]DayTotals, error) {
	var filter any
	if importID > 0 {
		filter = importID
	}
	rows, err := s.pool.Query(ctx, `
		select distinct on (d.day) d.day,d.visitors,d.visits,d.pageviews
		from imported_day d join imports i on i.id=d.import_id
		where d.site_id=$1 and d.day between $2 and $3 and i.status='completed'
		  and ($4::bigint is null or d.import_id=$4)
		order by d.day,i.completed_at desc nulls last,i.id desc`, siteID, from, to, filter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []DayTotals
	for rows.Next() {
		var day DayTotals
		if err := rows.Scan(&day.Day, &day.Visitors, &day.Visits, &day.Pageviews); err != nil {
			return nil, err
		}
		result = append(result, day)
	}
	return result, rows.Err()
}
