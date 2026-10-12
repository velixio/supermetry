package session

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	activityShardCount = 16
	activitySiteLimit  = 1024
	activityFeedLimit  = 100
	realtimeTopLimit   = 10
	realtimeMinutes    = 30
)

type activityIndex struct {
	shards [activityShardCount]activityShard
	total  atomic.Int32
}

type activityShard struct {
	mu    sync.RWMutex
	sites map[int32]*siteActivity
}

type siteActivity struct {
	mu      sync.Mutex
	minutes [realtimeMinutes]minuteActivity
	feed    [activityFeedLimit]RecentActivity
	next    int
	length  int
	lastNS  int64
	removed bool
}

type minuteActivity struct {
	minute    int64
	visitors  map[int64]visitorActivity
	pageviews int
	events    map[int32]int
}

type visitorActivity struct {
	firstNS int64
	lastNS  int64
}

type locationKey struct {
	id      int32
	country string
}

func (a *activityIndex) init() {
	for i := range a.shards {
		a.shards[i].sites = make(map[int32]*siteActivity)
	}
}

func (a *activityIndex) site(siteID int32, create bool) *siteActivity {
	sh := &a.shards[uint32(siteID)%activityShardCount]
	sh.mu.RLock()
	site := sh.sites[siteID]
	sh.mu.RUnlock()
	if site != nil || !create {
		return site
	}
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if site = sh.sites[siteID]; site != nil {
		return site
	}
	if a.total.Add(1) > activitySiteLimit {
		a.total.Add(-1)
		return nil
	}
	site = &siteActivity{}
	sh.sites[siteID] = site
	return site
}

// prune drops sites with no activity newer than cutoffNS so idle or deleted
// sites release their memory and their slot under activitySiteLimit.
func (a *activityIndex) prune(cutoffNS int64) {
	for i := range a.shards {
		sh := &a.shards[i]
		sh.mu.Lock()
		for id, site := range sh.sites {
			site.mu.Lock()
			if site.lastNS < cutoffNS {
				site.removed = true
				delete(sh.sites, id)
				a.total.Add(-1)
			}
			site.mu.Unlock()
		}
		sh.mu.Unlock()
	}
}

// RecordActivity records only pageviews and custom events. VisitorID stays in
// the bounded minute ring for exact-window de-duplication and is never copied
// to the feed.
func (m *Memory) RecordActivity(activity Activity) {
	if activity.Kind != 0 && activity.Kind != 1 {
		return
	}
	minute := activity.Timestamp.UTC().Truncate(time.Minute).Unix()
	index := int((minute / 60) % realtimeMinutes)
	if index < 0 {
		index += realtimeMinutes
	}
	var site *siteActivity
	for {
		site = m.activity.site(activity.SiteID, true)
		if site == nil {
			return
		}
		site.mu.Lock()
		if !site.removed {
			break
		}
		// Pruned between lookup and lock; retry against the live entry.
		site.mu.Unlock()
	}
	if ns := activity.Timestamp.UnixNano(); ns > site.lastNS {
		site.lastNS = ns
	}
	bucket := &site.minutes[index]
	if bucket.minute < minute {
		*bucket = minuteActivity{minute: minute, visitors: make(map[int64]visitorActivity)}
	}
	// A hit older than the slot's minute (clock skew, delayed delivery) must
	// not wipe newer data; it still reaches the feed below.
	if bucket.minute == minute {
		timestamp := activity.Timestamp.UnixNano()
		span, exists := bucket.visitors[activity.VisitorID]
		if !exists || timestamp < span.firstNS {
			span.firstNS = timestamp
		}
		if !exists || timestamp > span.lastNS {
			span.lastNS = timestamp
		}
		bucket.visitors[activity.VisitorID] = span
		if activity.Kind == 0 {
			bucket.pageviews++
		} else {
			if bucket.events == nil {
				bucket.events = make(map[int32]int)
			}
			bucket.events[activity.NameID]++
		}
	}
	site.feed[site.next] = RecentActivity{
		Kind: activity.Kind, PathID: activity.PathID, NameID: activity.NameID,
		CountryCode: activity.Country, CityID: activity.CityID, Device: activity.Device,
		BrowserID: activity.BrowserID, SourceID: activity.SourceID, Timestamp: activity.Timestamp,
	}
	site.next = (site.next + 1) % activityFeedLimit
	if site.length < activityFeedLimit {
		site.length++
	}
	site.mu.Unlock()
}

// ActiveVisitorsSince returns distinct visitors with an accepted pageview or
// custom event in the exact inclusive interval [since, until].
func (m *Memory) ActiveVisitorsSince(siteID int32, since, until time.Time) int {
	if until.Before(since) {
		return 0
	}
	site := m.activity.site(siteID, false)
	if site == nil {
		return 0
	}
	sinceNS, untilNS := since.UnixNano(), until.UnixNano()
	fromMinute := since.UTC().Truncate(time.Minute).Unix()
	toMinute := until.UTC().Truncate(time.Minute).Unix()
	visitors := make(map[int64]struct{})
	site.mu.Lock()
	for index := range site.minutes {
		bucket := &site.minutes[index]
		if bucket.minute < fromMinute || bucket.minute > toMinute {
			continue
		}
		for visitorID, span := range bucket.visitors {
			// The five-minute live window has distinct boundary buckets. Keep
			// both ends of each visitor's minute span so a future-skewed hit
			// cannot hide an earlier valid hit in the same minute.
			if bucket.minute == fromMinute && span.lastNS < sinceNS {
				continue
			}
			if bucket.minute == toMinute && span.firstNS > untilNS {
				continue
			}
			visitors[visitorID] = struct{}{}
		}
	}
	site.mu.Unlock()
	return len(visitors)
}

// RealtimeSnapshot returns a bounded, process-local view of the last thirty
// minutes. Session rankings and rolling event aggregates are computed once.
func (m *Memory) RealtimeSnapshot(siteID int32, now time.Time) RealtimeSnapshot {
	sinceNS := now.Add(-IdleTimeout).UnixNano()
	result := RealtimeSnapshot{}
	pageCounts := make(map[int32]int)
	entryCounts := make(map[int32]int)
	sourceCounts := make(map[int32]int)
	countryCounts := make(map[string]int)
	regionCounts := make(map[locationKey]int)
	cityCounts := make(map[locationKey]int)
	deviceCounts := make(map[int32]int)
	browserCounts := make(map[int32]int)
	osCounts := make(map[int32]int)
	for i := range m.shards {
		sh := &m.shards[i]
		sh.mu.Lock()
		for _, s := range sh.sessions {
			if s.siteID != siteID || s.lastNS < sinceNS || s.lastNS > now.UnixNano() {
				continue
			}
			result.ActiveVisitors++
			incrementID(pageCounts, s.exit)
			incrementID(entryCounts, s.entry)
			incrementID(sourceCounts, s.source)
			country := decodeCountry(s.country)
			if country != "" {
				countryCounts[country]++
			}
			if s.region != 0 {
				regionCounts[locationKey{s.region, country}]++
			}
			if s.city != 0 {
				cityCounts[locationKey{s.city, country}]++
			}
			deviceCounts[int32(s.device)]++
			incrementID(browserCounts, s.browser)
			incrementID(osCounts, s.os)
		}
		sh.mu.Unlock()
	}
	result.Pages = rankActiveValuesLimit(pageCounts, realtimeTopLimit)
	result.EntryPages = rankActiveValuesLimit(entryCounts, realtimeTopLimit)
	result.Sources = rankActiveValuesLimit(sourceCounts, realtimeTopLimit)
	result.Countries = rankCountries(countryCounts)
	result.Regions = rankLocations(regionCounts)
	result.Cities = rankLocations(cityCounts)
	result.Devices = rankActiveValuesLimit(deviceCounts, realtimeTopLimit)
	result.Browsers = rankActiveValuesLimit(browserCounts, realtimeTopLimit)
	result.OperatingSystems = rankActiveValuesLimit(osCounts, realtimeTopLimit)
	m.snapshotActivity(siteID, now, &result)
	return result
}

func incrementID(counts map[int32]int, id int32) {
	if id != 0 {
		counts[id]++
	}
}

func rankActiveValuesLimit(counts map[int32]int, limit int) []ActiveValue {
	values := rankActiveValues(counts)
	if len(values) > limit {
		values = values[:limit]
	}
	return values
}

func rankCountries(counts map[string]int) []ActiveCountry {
	values := make([]ActiveCountry, 0, len(counts))
	for country, visitors := range counts {
		values = append(values, ActiveCountry{country, visitors})
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Visitors != values[j].Visitors {
			return values[i].Visitors > values[j].Visitors
		}
		return values[i].CountryCode < values[j].CountryCode
	})
	if len(values) > realtimeTopLimit {
		values = values[:realtimeTopLimit]
	}
	return values
}

func rankLocations(counts map[locationKey]int) []ActiveLocation {
	values := make([]ActiveLocation, 0, len(counts))
	for key, visitors := range counts {
		values = append(values, ActiveLocation{key.id, key.country, visitors})
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Visitors != values[j].Visitors {
			return values[i].Visitors > values[j].Visitors
		}
		if values[i].CountryCode != values[j].CountryCode {
			return values[i].CountryCode < values[j].CountryCode
		}
		return values[i].ID < values[j].ID
	})
	if len(values) > realtimeTopLimit {
		values = values[:realtimeTopLimit]
	}
	return values
}

func (m *Memory) snapshotActivity(siteID int32, now time.Time, result *RealtimeSnapshot) {
	currentMinute := now.UTC().Truncate(time.Minute)
	result.VisitorsPerMinute = make([]MinuteVisitors, realtimeMinutes)
	site := m.activity.site(siteID, false)
	if site == nil {
		for i := range realtimeMinutes {
			result.VisitorsPerMinute[i].Minute = currentMinute.Add(time.Duration(i-realtimeMinutes+1) * time.Minute)
		}
		return
	}
	eventCounts := make(map[int32]int)
	site.mu.Lock()
	for i := range realtimeMinutes {
		minute := currentMinute.Add(time.Duration(i-realtimeMinutes+1) * time.Minute)
		result.VisitorsPerMinute[i].Minute = minute
		index := int((minute.Unix() / 60) % realtimeMinutes)
		if index < 0 {
			index += realtimeMinutes
		}
		bucket := &site.minutes[index]
		if bucket.minute != minute.Unix() {
			continue
		}
		result.VisitorsPerMinute[i].Visitors = len(bucket.visitors)
		result.Pageviews += bucket.pageviews
		for name, count := range bucket.events {
			eventCounts[name] += count
		}
	}
	cutoff := now.Add(-IdleTimeout)
	for offset := 0; offset < site.length; offset++ {
		index := site.next - 1 - offset
		if index < 0 {
			index += activityFeedLimit
		}
		entry := site.feed[index]
		if entry.Timestamp.Before(cutoff) || entry.Timestamp.After(now) {
			continue
		}
		result.RecentActivity = append(result.RecentActivity, entry)
	}
	site.mu.Unlock()
	sort.SliceStable(result.RecentActivity, func(i, j int) bool {
		return result.RecentActivity[i].Timestamp.After(result.RecentActivity[j].Timestamp)
	})
	if len(result.RecentActivity) > 50 {
		result.RecentActivity = result.RecentActivity[:50]
	}
	result.Events = make([]ActiveEvent, 0, len(eventCounts))
	for name, count := range eventCounts {
		result.Events = append(result.Events, ActiveEvent{name, count})
	}
	sort.Slice(result.Events, func(i, j int) bool {
		if result.Events[i].Count != result.Events[j].Count {
			return result.Events[i].Count > result.Events[j].Count
		}
		return result.Events[i].NameID < result.Events[j].NameID
	})
	if len(result.Events) > realtimeTopLimit {
		result.Events = result.Events[:realtimeTopLimit]
	}
}
