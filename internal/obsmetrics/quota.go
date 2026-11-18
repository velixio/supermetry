package obsmetrics

import (
	"sync"
	"time"
)

// DefaultDailyQuota is the metric points a site may send per UTC day.
const DefaultDailyQuota = 50_000_000

// Quota counts accepted points per site per UTC day in memory. A limit of 0
// means unlimited. The count restarts with the process, which only ever makes
// it more lenient.
type Quota struct {
	mu    sync.Mutex
	limit int64
	days  map[int32]quotaDay
}

type quotaDay struct {
	day  string
	used int64
}

// NewQuota builds a tracker; limit 0 disables it.
func NewQuota(limit int64) *Quota { return &Quota{limit: limit, days: map[int32]quotaDay{}} }

// Take reserves up to n points for the site and returns how many fit.
func (q *Quota) Take(siteID int32, n int, now time.Time) int {
	if q == nil || q.limit <= 0 {
		return n
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	day := now.UTC().Format(time.DateOnly)
	d := q.days[siteID]
	if d.day != day {
		d = quotaDay{day: day}
	}
	room := q.limit - d.used
	if room < 0 {
		room = 0
	}
	fit := int(min(int64(n), room))
	d.used += int64(fit)
	q.days[siteID] = d
	return fit
}
