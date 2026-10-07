package core

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unique"
)

// Session is one visit. Times are unix microseconds. One lives in memory per
// active visitor until 30 min after their last event, so at 1K events/s
// about 550K are live (R2) and the struct is kept at 80 B (one size class,
// no other per-session allocation):
//   - the session ID is not stored but derived from (visitor, start), see ID;
//   - entry, exit and source are canonical strings (unique.Handle, 8 B):
//     sessions share one copy of each path/host instead of holding the
//     per-request string, and unused values are reclaimed by the GC;
//   - visitor-constant dims are 2-byte interned IDs, the day is a uint16;
//   - the rare UTM fields plus rollup-stream's path list sit behind a pointer.
//
// Measured with TestSessionMemory: 206 B/session before (128 B struct +
// separate path/source strings), about 100 B after (80 B + map slot).
type Session struct {
	VisitorID                    int64
	Start                        int64
	Last                         int64
	entry, exit, source          unique.Handle[string]
	ext                          *sessionExt // nil unless UTM, paths, or a restored ID override is needed
	SiteID                       int32
	Pageviews                    int32
	Custom                       int32
	day                          uint16 // Day (days since 1970) fits until 2149
	country, browser, os, device uint16
	queued                       bool // in the shard's dirty list
}

type sessionExt struct {
	utmSource, utmMedium, utmCampaign string
	paths                             []string
	sessionID                         int64
	hasSessionID                      bool
}

// SessionState is the persisted state needed to rehydrate an open session
// after a process restart. It deliberately contains no IP or user agent.
type SessionState struct {
	SiteID, Pageviews, Custom         int32
	VisitorID, Start, Last            int64
	Day                               Day
	Entry, Exit, Source               string
	Country, Browser, OS, Device      string
	UTMSource, UTMMedium, UTMCampaign string
}

// RestoreSession rebuilds the compact in-memory representation from a
// persisted session row.
func RestoreSession(v SessionState) Session {
	s := Session{
		VisitorID: v.VisitorID, Start: v.Start, Last: v.Last, SiteID: v.SiteID,
		Pageviews: v.Pageviews, Custom: v.Custom, day: uint16(v.Day),
		entry: handle(v.Entry), exit: handle(v.Exit), source: handle(v.Source),
		country: dims.id(v.Country), browser: dims.id(v.Browser), os: dims.id(v.OS), device: dims.id(v.Device),
	}
	if v.UTMSource != "" || v.UTMMedium != "" || v.UTMCampaign != "" {
		s.ext = &sessionExt{utmSource: v.UTMSource, utmMedium: v.UTMMedium, utmCampaign: v.UTMCampaign}
	}
	return s
}

// RebuildSession reconstructs one session from its durable events. Events
// must belong to the same session and be ordered by timestamp. The event log,
// rather than a possibly stale sessions snapshot, is authoritative after a
// process crash.
func RebuildSession(events []Event) (Session, error) {
	if len(events) == 0 {
		return Session{}, fmt.Errorf("rebuild session: no events")
	}
	first := &events[0]
	s := Session{
		VisitorID: first.VisitorID, Start: first.TS, Last: first.TS,
		SiteID: first.SiteID, day: uint16(first.Day), source: handle(first.Source),
		country: dims.id(first.Country), browser: dims.id(first.Browser), os: dims.id(first.OS), device: dims.id(first.Device),
	}
	if first.UTMSource != "" || first.UTMMedium != "" || first.UTMCampaign != "" {
		s.ext = &sessionExt{utmSource: first.UTMSource, utmMedium: first.UTMMedium, utmCampaign: first.UTMCampaign}
	}
	if s.ID() != first.SessionID {
		if s.ext == nil {
			s.ext = &sessionExt{}
		}
		s.ext.sessionID = first.SessionID
		s.ext.hasSessionID = true
	}
	last := first.TS
	for i := range events {
		ev := &events[i]
		if ev.SiteID != first.SiteID || ev.VisitorID != first.VisitorID || ev.SessionID != first.SessionID || ev.Day != first.Day {
			return Session{}, fmt.Errorf("rebuild session: event %d belongs to another session", i)
		}
		if ev.TS < last {
			return Session{}, fmt.Errorf("rebuild session: event %d is out of order", i)
		}
		s.apply(ev, false)
		last = ev.TS
	}
	return s, nil
}

var noHandle unique.Handle[string]

func handle(s string) unique.Handle[string] {
	if s == "" {
		return noHandle
	}
	return unique.Make(s)
}

func hstr(h unique.Handle[string]) string {
	if h == noHandle {
		return ""
	}
	return h.Value()
}

// ID is the session's key, a 63-bit mix of (visitor ID, start µs). A visitor
// has at most one session starting at a given microsecond, so IDs only
// collide by hash chance (2^-63 per pair; rows are also keyed by site and
// start_ts). Deriving it saves 8 B per cached session; it is stable for the
// session's life and across restarts of the same session.
func (s *Session) ID() int64 {
	if s.ext != nil && s.ext.hasSessionID {
		return s.ext.sessionID
	}
	return sessionID(s.VisitorID, s.Start)
}

func sessionID(visitor, start int64) int64 {
	x := uint64(visitor) ^ (uint64(start) * 0x9E3779B97F4A7C15)
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return int64(x >> 1)
}

// Day is the session's site-local date.
func (s *Session) Day() Day { return Day(s.day) }

// Entry and Exit are the first and last pageview paths ("" if none); Source
// is the session's traffic source.
func (s *Session) Entry() string  { return hstr(s.entry) }
func (s *Session) Exit() string   { return hstr(s.exit) }
func (s *Session) Source() string { return hstr(s.source) }

// Country, Browser, OS, Device, UTM* and Paths are accessors for the compact fields.
func (s *Session) Country() string { return dims.str(s.country) }
func (s *Session) Browser() string { return dims.str(s.browser) }
func (s *Session) OS() string      { return dims.str(s.os) }
func (s *Session) Device() string  { return dims.str(s.device) }
func (s *Session) UTMSource() string {
	if s.ext == nil {
		return ""
	}
	return s.ext.utmSource
}
func (s *Session) UTMMedium() string {
	if s.ext == nil {
		return ""
	}
	return s.ext.utmMedium
}
func (s *Session) UTMCampaign() string {
	if s.ext == nil {
		return ""
	}
	return s.ext.utmCampaign
}

// Paths are the distinct pageview paths (only with CacheConfig.TrackPaths).
func (s *Session) Paths() []string {
	if s.ext == nil {
		return nil
	}
	return s.ext.paths
}

// Bounce: exactly one pageview and no custom events (CONTRACT).
func (s *Session) Bounce() bool { return s.Pageviews == 1 && s.Custom == 0 }

// DurationMs is last − first event time.
func (s *Session) DurationMs() int64 { return (s.Last - s.Start) / 1000 }

const nShards = 64

type shard struct {
	mu    sync.Mutex
	m     map[int64]*Session // keyed by visitor ID: a visitor has one live session
	dirty []*Session
	_     [32]byte // keep hot shard headers on separate cache lines
}

// CacheConfig configures a Cache.
type CacheConfig struct {
	Timeout    time.Duration  // idle timeout; default SessionTimeout
	TrackPaths bool           // keep distinct paths per session (rollup-stream)
	NoDirty    bool           // don't keep dirty lists (rollup-stream never upserts)
	OnRetire   func(*Session) // called under the shard lock when a session leaves the cache
}

// Cache is the sharded in-memory session store (D8). A visitor's next event
// continues its session if it arrives within the idle timeout; otherwise a
// new session starts. Visitor IDs change at site-local midnight (new salt),
// so sessions never cross it.
type Cache struct {
	cfg       CacheConfig
	timeout   int64
	shards    [nShards]shard
	watermark atomic.Int64
}

// NewCache returns an empty cache.
func NewCache(cfg CacheConfig) *Cache {
	if cfg.Timeout == 0 {
		cfg.Timeout = SessionTimeout
	}
	c := &Cache{cfg: cfg, timeout: cfg.Timeout.Microseconds()}
	for i := range c.shards {
		c.shards[i].m = make(map[int64]*Session)
	}
	return c
}

func (c *Cache) shardFor(vid int64) *shard { return &c.shards[uint64(vid)%nShards] }

// Restore installs a persisted open session without marking it dirty. If a
// newer state for the visitor is already present, it is kept.
func (c *Cache) Restore(s Session) bool {
	sh := c.shardFor(s.VisitorID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if cur := sh.m[s.VisitorID]; cur != nil && cur.Last >= s.Last {
		return false
	}
	s.queued = false
	sh.m[s.VisitorID] = &s
	c.bumpWatermark(s.Last)
	return true
}

// Assign attaches ev to its visitor's session, creating one if needed, and
// copies the session dimensions onto ev. commit (may be nil) runs while the
// shard is locked, after ev is filled in; if it returns false the session is
// left exactly as it was and Assign returns false. This makes "enqueue or
// reject with 503" atomic with the session update.
func (c *Cache) Assign(ev *Event, commit func(*Event) bool) bool {
	sh := c.shardFor(ev.VisitorID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	s := sh.m[ev.VisitorID]
	if s != nil && ev.TS-s.Last <= c.timeout && s.day == uint16(ev.Day) {
		ev.SessionID = s.ID()
		s.copyDims(ev)
		if commit != nil && !commit(ev) {
			return false
		}
		s.apply(ev, c.cfg.TrackPaths)
		c.markDirty(sh, s)
		c.bumpWatermark(ev.TS)
		return true
	}
	ns := &Session{
		VisitorID: ev.VisitorID, Start: ev.TS, Last: ev.TS,
		SiteID: ev.SiteID, day: uint16(ev.Day), source: handle(ev.Source),
		country: dims.id(ev.Country), browser: dims.id(ev.Browser), os: dims.id(ev.OS), device: dims.id(ev.Device),
	}
	if ev.UTMSource != "" || ev.UTMMedium != "" || ev.UTMCampaign != "" || c.cfg.TrackPaths {
		ns.ext = &sessionExt{utmSource: ev.UTMSource, utmMedium: ev.UTMMedium, utmCampaign: ev.UTMCampaign}
	}
	ev.SessionID = ns.ID()
	if commit != nil && !commit(ev) {
		return false
	}
	if s != nil {
		c.retire(s)
	}
	ns.apply(ev, c.cfg.TrackPaths)
	sh.m[ev.VisitorID] = ns
	c.markDirty(sh, ns)
	c.bumpWatermark(ev.TS)
	return true
}

func (s *Session) copyDims(ev *Event) {
	ev.Source, ev.UTMSource, ev.UTMMedium, ev.UTMCampaign = s.Source(), s.UTMSource(), s.UTMMedium(), s.UTMCampaign()
	ev.Country, ev.Browser, ev.OS, ev.Device = s.Country(), s.Browser(), s.OS(), s.Device()
}

func (s *Session) apply(ev *Event, trackPaths bool) {
	if ev.TS > s.Last {
		s.Last = ev.TS
	}
	if !ev.IsPageview() {
		s.Custom++
		return
	}
	s.Pageviews++
	h := handle(ev.Path)
	if s.Pageviews == 1 {
		s.entry = h
	}
	s.exit = h
	if trackPaths {
		for _, p := range s.ext.paths {
			if p == ev.Path {
				return
			}
		}
		s.ext.paths = append(s.ext.paths, ev.Path)
	}
}

func (c *Cache) markDirty(sh *shard, s *Session) {
	if c.cfg.NoDirty || s.queued {
		return
	}
	s.queued = true
	sh.dirty = append(sh.dirty, s)
}

func (c *Cache) retire(s *Session) {
	if c.cfg.OnRetire != nil {
		c.cfg.OnRetire(s)
	}
}

func (c *Cache) bumpWatermark(ts int64) {
	for {
		w := c.watermark.Load()
		if ts <= w || c.watermark.CompareAndSwap(w, ts) {
			return
		}
	}
}

// Watermark is the largest event time seen (the clock for bulk load).
func (c *Cache) Watermark() int64 { return c.watermark.Load() }

// NumShards is the number of cache shards (for CollectShard).
const NumShards = nShards

// Collect returns copies of all sessions changed since the previous call. If
// evict is set, sessions idle for longer than the timeout at now are removed
// (after being included if dirty).
func (c *Cache) Collect(now int64, evict bool) []Session {
	var out []Session
	for i := range c.shards {
		out = c.CollectShard(i, now, evict, out)
	}
	return out
}

// CollectShard is Collect for one shard, appending to out.
func (c *Cache) CollectShard(i int, now int64, evict bool, out []Session) []Session {
	sh := &c.shards[i]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	for _, s := range sh.dirty {
		s.queued = false
		out = append(out, *s) // ext is immutable here (paths only change in rollup-stream, which never collects)
	}
	clear(sh.dirty)
	sh.dirty = sh.dirty[:0]
	if evict {
		for k, s := range sh.m {
			if now-s.Last > c.timeout {
				delete(sh.m, k)
				c.retire(s)
			}
		}
	}
	return out
}

// EvictIf removes (and retires) every session for which pred is true.
func (c *Cache) EvictIf(pred func(*Session) bool) int {
	n := 0
	for i := range c.shards {
		sh := &c.shards[i]
		sh.mu.Lock()
		for k, s := range sh.m {
			if pred(s) {
				delete(sh.m, k)
				c.retire(s)
				n++
			}
		}
		sh.mu.Unlock()
	}
	return n
}

// RetireAll evicts every session.
func (c *Cache) RetireAll() { c.EvictIf(func(*Session) bool { return true }) }

// Active counts visitors of site with an event at or after since (realtime).
func (c *Cache) Active(site int32, since int64) int {
	n := 0
	for i := range c.shards {
		sh := &c.shards[i]
		sh.mu.Lock()
		for _, s := range sh.m {
			if s.SiteID == site && s.Last >= since {
				n++
			}
		}
		sh.mu.Unlock()
	}
	return n
}

// Len returns the number of live sessions.
func (c *Cache) Len() int {
	n := 0
	for i := range c.shards {
		sh := &c.shards[i]
		sh.mu.Lock()
		n += len(sh.m)
		sh.mu.Unlock()
	}
	return n
}
