package links

import (
	"context"
	"sync"
	"time"
)

// SiteViews keeps each site's pixel impressions in memory, across all campaign tags, so a
// badge never reads the database. Campaign tags are chosen by whoever requests the image, so
// they are deliberately not tracked here: the memory stays bounded by sites times days.
type SiteViews struct {
	mu    sync.RWMutex
	total map[int32]int64
	days  map[int32]map[string]int64
}

func NewSiteViews() *SiteViews {
	return &SiteViews{total: map[int32]int64{}, days: map[int32]map[string]int64{}}
}

// Add counts impressions on a site-local day.
func (v *SiteViews) Add(siteID int32, day string, n int64) {
	v.mu.Lock()
	v.total[siteID] += n
	if v.days[siteID] == nil {
		v.days[siteID] = map[string]int64{}
	}
	v.days[siteID][day] += n
	v.mu.Unlock()
}

// Seed loads persisted totals and the per-day counts since a date (YYYY-MM-DD),
// keeping the larger value so impressions counted before seeding are not lost.
func (v *SiteViews) Seed(ctx context.Context, db DBTX, since string) error {
	totals, err := db.Query(ctx, `select site_id, sum(impressions)::bigint from pixel_impressions_daily group by site_id`)
	if err != nil {
		return err
	}
	persisted := map[int32]int64{}
	for totals.Next() {
		var id int32
		var n int64
		if err := totals.Scan(&id, &n); err != nil {
			totals.Close()
			return err
		}
		persisted[id] = n
	}
	totals.Close()
	if err := totals.Err(); err != nil {
		return err
	}
	days, err := db.Query(ctx, `select site_id, day::text, sum(impressions)::bigint from pixel_impressions_daily
		where day >= $1::date group by site_id, day`, since)
	if err != nil {
		return err
	}
	defer days.Close()
	recent := map[int32]map[string]int64{}
	for days.Next() {
		var id int32
		var day string
		var n int64
		if err := days.Scan(&id, &day, &n); err != nil {
			return err
		}
		if recent[id] == nil {
			recent[id] = map[string]int64{}
		}
		recent[id][day] = n
	}
	if err := days.Err(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for id, n := range persisted {
		if n > v.total[id] {
			v.total[id] = n
		}
	}
	for id, byDay := range recent {
		if v.days[id] == nil {
			v.days[id] = map[string]int64{}
		}
		for day, n := range byDay {
			if n > v.days[id][day] {
				v.days[id][day] = n
			}
		}
	}
	return nil
}

// Snapshot returns the all-time total and the last n days ending at today, oldest first.
func (v *SiteViews) Snapshot(siteID int32, today string, n int) (int64, []int64) {
	series := make([]int64, n)
	end, err := time.Parse(time.DateOnly, today)
	v.mu.RLock()
	defer v.mu.RUnlock()
	if err == nil {
		for i := range series {
			series[i] = v.days[siteID][end.AddDate(0, 0, i-n+1).Format(time.DateOnly)]
		}
	}
	return v.total[siteID], series
}
