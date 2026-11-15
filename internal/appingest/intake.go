package appingest

import (
	"hash/fnv"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"supermetry/internal/dropstats"
	"supermetry/internal/health"
	"supermetry/internal/salts"
	"supermetry/internal/sites"
)

const (
	// DedupTTL is how long an event id counts as already received.
	DedupTTL = 15 * time.Minute

	defaultQueueSize      = 4096
	defaultKeyBudget      = 600 // events per minute per ingest key
	defaultDedupEntries   = 200_000
	tagValueCap           = 1000 // distinct values per tag key per app per UTC day
	tagKeyCap             = 100  // distinct tag keys per app per UTC day
	otherValue            = "(other)"
	futureSkew            = 5 * time.Minute
	maxTrackedBudgetKeys  = 100_000
	budgetIdleBeforeEvict = 5 * time.Minute
)

// Drop reasons as clients see them in a reply; the closed list of the wire format.
const (
	ReasonTooOld        = "too_old"
	ReasonInvalid       = "invalid"
	ReasonTagNotAllowed = "tag_not_allowed"
	ReasonRateLimited   = "rate_limited"
	ReasonDuplicate     = "duplicate"
)

// WireReason maps a drop-statistics reason to the name clients see.
func WireReason(reason dropstats.Reason) string {
	switch reason {
	case dropstats.TooOld:
		return ReasonTooOld
	case dropstats.AppDuplicate:
		return ReasonDuplicate
	case dropstats.AppRateLimited:
		return ReasonRateLimited
	default:
		return ReasonInvalid
	}
}

// Config tunes an Intake. Zero values use the defaults.
type Config struct {
	QueueSize          int
	KeyBudgetPerMinute int
	DedupEntries       int
	DropStats          *dropstats.Recorder
}

// Queued is an accepted event on its way to the writer. Secret is the app's
// hash secret when the registry already knows it; the writer creates it
// otherwise.
type Queued struct {
	SiteID     int32
	ReceivedAt time.Time
	Secret     []byte
	Event      Event
}

// Outcome is the result of one event. Reason is empty when accepted; Stripped
// lists tags removed because they are not on the app's allowlist.
type Outcome struct {
	Reason   string
	Stripped []string
}

// Intake applies the per-event checks and hands accepted events to a bounded
// queue. It never touches the database.
type Intake struct {
	ch        chan Queued
	closed    atomic.Bool
	dropStats *dropstats.Recorder
	dedup     *ttlSet
	budgets   *budgetMap
	tagCaps   *tagCaps
}

// NewIntake builds an intake with a bounded queue.
func NewIntake(cfg Config) *Intake {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultQueueSize
	}
	if cfg.KeyBudgetPerMinute <= 0 {
		cfg.KeyBudgetPerMinute = defaultKeyBudget
	}
	if cfg.DedupEntries <= 0 {
		cfg.DedupEntries = defaultDedupEntries
	}
	return &Intake{
		ch:        make(chan Queued, cfg.QueueSize),
		dropStats: cfg.DropStats,
		dedup:     newTTLSet(cfg.DedupEntries, DedupTTL),
		budgets:   newBudgetMap(float64(cfg.KeyBudgetPerMinute), time.Minute),
		tagCaps:   newTagCaps(),
	}
}

// Queue is the receive side of the queue, for the writer.
func (in *Intake) Queue() <-chan Queued { return in.ch }

// Drop records a dropped event in the site's drop statistics.
func (in *Intake) Drop(site sites.Site, now time.Time, reason dropstats.Reason) {
	if in == nil || in.dropStats == nil {
		return
	}
	in.dropStats.Record(site.ID, salts.LocalDay(now, site.TZ), reason, "")
}

// Accept applies the per-event checks to one parsed event for an app site.
// budgetKey identifies the ingest key (or the app, for mapped events).
func (in *Intake) Accept(site sites.Site, budgetKey string, now time.Time, ev Event) Outcome {
	app := site.App
	if app == nil {
		return Outcome{Reason: ReasonInvalid}
	}
	drop := func(reason dropstats.Reason) Outcome {
		in.Drop(site, now, reason)
		return Outcome{Reason: WireReason(reason)}
	}
	if !ev.TS.IsZero() && ev.TS.Before(now.Add(-maxAge(site))) {
		return drop(dropstats.TooOld)
	}
	if ev.TS.IsZero() || ev.TS.After(now.Add(futureSkew)) {
		if !ev.TS.IsZero() {
			health.IngestClockSkew.Add(1)
		}
		ev.TS = now
	}
	if in.dedup.seen(dedupKey{site.ID, ev.ID}, now) {
		return drop(dropstats.AppDuplicate)
	}
	release := func() { in.dedup.release(dedupKey{site.ID, ev.ID}) }
	if !in.budgets.take(budgetKey, now) {
		release()
		return drop(dropstats.AppRateLimited)
	}
	if len(app.Environments) > 0 && ev.Environment != "" && !contains(app.Environments, ev.Environment) {
		ev.Environment = otherValue
	}
	var stripped []string
	ev.Tags, stripped = in.filterTags(site.ID, app, ev.Tags, now)
	queued := Queued{SiteID: site.ID, ReceivedAt: now, Secret: app.HashSecret, Event: ev}
	if in.closed.Load() || !in.enqueue(queued) {
		release()
		health.AppIngestQueueFull.Add(1)
		return drop(dropstats.AppRateLimited)
	}
	return Outcome{Stripped: stripped}
}

func (in *Intake) enqueue(q Queued) bool {
	select {
	case in.ch <- q:
		return true
	default:
		return false
	}
}

// maxAge is the oldest report an app accepts: the shorter of its max report
// age and its retention.
func maxAge(site sites.Site) time.Duration {
	days := site.App.MaxReportAgeDays
	if days <= 0 {
		days = 7
	}
	if retention := int(site.Retention); retention > 0 && retention < days {
		days = retention
	}
	return time.Duration(days) * 24 * time.Hour
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// filterTags removes tags outside a non-empty allowlist, then folds values
// past the per-day cardinality cap into "(other)".
func (in *Intake) filterTags(siteID int32, app *sites.AppConfig, tags map[string]string, now time.Time) (map[string]string, []string) {
	if len(tags) == 0 {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(tags))
	var stripped []string
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if len(app.TagAllowlist) > 0 && !contains(app.TagAllowlist, key) {
			stripped = append(stripped, key)
			continue
		}
		value, ok := in.tagCaps.admit(siteID, key, tags[key], now)
		if ok {
			out[key] = value
		}
	}
	return out, stripped
}

// close makes later Accept calls report a full queue; the writer calls it
// before its final drain.
func (in *Intake) close() { in.closed.Store(true) }

// --- event id window ---

type dedupKey struct {
	siteID int32
	id     string
}

// ttlSet remembers keys for about ttl using two rotating generations, which
// bounds memory without a per-key timer.
type ttlSet struct {
	mu      sync.Mutex
	max     int
	ttl     time.Duration
	rotated time.Time
	cur     map[dedupKey]time.Time
	prev    map[dedupKey]time.Time
}

func newTTLSet(max int, ttl time.Duration) *ttlSet {
	return &ttlSet{max: max, ttl: ttl, cur: map[dedupKey]time.Time{}, prev: map[dedupKey]time.Time{}}
}

// seen reports whether the key is inside the window and records it if not.
func (s *ttlSet) seen(key dedupKey, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rotated.IsZero() {
		s.rotated = now
	}
	if now.Sub(s.rotated) >= s.ttl || len(s.cur) >= s.max {
		s.prev, s.cur, s.rotated = s.cur, make(map[dedupKey]time.Time, len(s.prev)), now
	}
	for _, m := range []map[dedupKey]time.Time{s.cur, s.prev} {
		if at, ok := m[key]; ok && now.Sub(at) < s.ttl {
			return true
		}
	}
	s.cur[key] = now
	return false
}

func (s *ttlSet) release(key dedupKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cur, key)
	delete(s.prev, key)
}

// --- per-key event budget ---

type bucket struct {
	tokens float64
	last   time.Time
}

type budgetMap struct {
	mu       sync.Mutex
	capacity float64
	perSec   float64
	buckets  map[string]*bucket
}

func newBudgetMap(capacity float64, per time.Duration) *budgetMap {
	return &budgetMap{capacity: capacity, perSec: capacity / per.Seconds(), buckets: map[string]*bucket{}}
}

func (b *budgetMap) take(key string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	cell, ok := b.buckets[key]
	if !ok {
		if len(b.buckets) >= maxTrackedBudgetKeys {
			for k, v := range b.buckets {
				if now.Sub(v.last) > budgetIdleBeforeEvict {
					delete(b.buckets, k)
				}
			}
		}
		cell = &bucket{tokens: b.capacity, last: now}
		b.buckets[key] = cell
	}
	if elapsed := now.Sub(cell.last).Seconds(); elapsed > 0 {
		cell.tokens = min(b.capacity, cell.tokens+elapsed*b.perSec)
		cell.last = now
	}
	if cell.tokens < 1 {
		return false
	}
	cell.tokens--
	return true
}

// --- tag cardinality ---

type dayTags struct {
	day    int64
	values map[string]map[uint64]struct{}
}

type tagCaps struct {
	mu    sync.Mutex
	sites map[int32]*dayTags
}

func newTagCaps() *tagCaps { return &tagCaps{sites: map[int32]*dayTags{}} }

// admit returns the value to store for a tag, or ok=false when the tag key
// itself is past the key cap. Values beyond the per-key cap become "(other)".
func (c *tagCaps) admit(siteID int32, key, value string, now time.Time) (string, bool) {
	day := now.UTC().Unix() / 86400
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.sites[siteID]
	if state == nil || state.day != day {
		state = &dayTags{day: day, values: map[string]map[uint64]struct{}{}}
		c.sites[siteID] = state
	}
	seen := state.values[key]
	if seen == nil {
		if len(state.values) >= tagKeyCap {
			return "", false
		}
		seen = map[uint64]struct{}{}
		state.values[key] = seen
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(value))
	sum := h.Sum64()
	if _, ok := seen[sum]; ok {
		return value, true
	}
	if len(seen) >= tagValueCap {
		return otherValue, true
	}
	seen[sum] = struct{}{}
	return value, true
}
