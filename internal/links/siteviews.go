package links

import (
	"context"
	"sync"
	"time"
)

type siteViewKey struct {
	siteID int32
	tag    string
}

type siteViewEntry struct {
	total int64
	days  map[string]int64
}

// SiteViews keeps badge totals for explicitly registered tracking-pixel tags.
// Arbitrary public query parameters never allocate cache entries.
type SiteViews struct {
	reloadMu sync.Mutex
	mu       sync.RWMutex
	entries  map[siteViewKey]*siteViewEntry
}

func NewSiteViews() *SiteViews {
	return &SiteViews{entries: make(map[siteViewKey]*siteViewEntry)}
}

func (v *SiteViews) Register(siteID int32, tag string) {
	v.reloadMu.Lock()
	defer v.reloadMu.Unlock()
	v.mu.Lock()
	defer v.mu.Unlock()
	key := siteViewKey{siteID: siteID, tag: tag}
	if v.entries[key] == nil {
		v.entries[key] = &siteViewEntry{days: make(map[string]int64)}
	}
}

func (v *SiteViews) Unregister(siteID int32, tag string) {
	v.reloadMu.Lock()
	defer v.reloadMu.Unlock()
	v.mu.Lock()
	delete(v.entries, siteViewKey{siteID: siteID, tag: tag})
	v.mu.Unlock()
}

// Registered reports whether a site and tag are in the in-memory registry.
func (v *SiteViews) Registered(siteID int32, tag string) bool {
	if v == nil || tag == "" {
		return false
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.entries[siteViewKey{siteID: siteID, tag: tag}] != nil
}

// Add counts an impression only when its site and tag are registered.
func (v *SiteViews) Add(siteID int32, tag, day string, n int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	entry := v.entries[siteViewKey{siteID: siteID, tag: tag}]
	if entry == nil {
		return
	}
	entry.total += n
	entry.days[day] += n
	if end, err := time.Parse(time.DateOnly, day); err == nil {
		oldest := end.AddDate(0, 0, -35)
		for stored := range entry.days {
			parsed, err := time.Parse(time.DateOnly, stored)
			if err == nil && parsed.Before(oldest) {
				delete(entry.days, stored)
			}
		}
	}
}

// Reload replaces the registered set from storage and seeds its all-time and
// recent counts. Max-merging preserves impressions counted during the load.
func (v *SiteViews) Reload(ctx context.Context, db DBTX) error {
	v.reloadMu.Lock()
	defer v.reloadMu.Unlock()

	rows, err := db.Query(ctx, `
		select tp.site_id, tp.tag, coalesce(t.total,0)::bigint, d.day::text, d.impressions
		from tracking_pixels tp
		left join lateral (
			select sum(impressions)::bigint as total from pixel_impressions_daily
			where site_id=tp.site_id and campaign=tp.tag
		) t on true
		left join lateral (
			select day, sum(impressions)::bigint as impressions from pixel_impressions_daily
			where site_id=tp.site_id and campaign=tp.tag and day >= current_date - 30
			group by day
		) d on true`)
	if err != nil {
		return err
	}
	defer rows.Close()
	loaded := make(map[siteViewKey]*siteViewEntry)
	for rows.Next() {
		var siteID int32
		var tag string
		var total int64
		var day *string
		var impressions *int64
		if err := rows.Scan(&siteID, &tag, &total, &day, &impressions); err != nil {
			return err
		}
		key := siteViewKey{siteID: siteID, tag: tag}
		entry := loaded[key]
		if entry == nil {
			entry = &siteViewEntry{total: total, days: make(map[string]int64)}
			loaded[key] = entry
		}
		if day != nil && impressions != nil {
			entry.days[*day] = *impressions
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	for key, fresh := range loaded {
		if current := v.entries[key]; current != nil {
			if current.total > fresh.total {
				fresh.total = current.total
			}
			for day, count := range current.days {
				if count > fresh.days[day] {
					fresh.days[day] = count
				}
			}
		}
	}
	v.entries = loaded
	return nil
}

// Snapshot returns totals for one registered site and tag and the last n days
// ending at today, oldest first.
func (v *SiteViews) Snapshot(siteID int32, tag, today string, n int) (int64, []int64, bool) {
	series := make([]int64, n)
	end, err := time.Parse(time.DateOnly, today)
	v.mu.RLock()
	defer v.mu.RUnlock()
	entry := v.entries[siteViewKey{siteID: siteID, tag: tag}]
	if entry == nil {
		return 0, series, false
	}
	if err == nil {
		for i := range series {
			series[i] = entry.days[end.AddDate(0, 0, i-n+1).Format(time.DateOnly)]
		}
	}
	return entry.total, series, true
}
