package session

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	mathrand "math/rand/v2"
	"sort"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"panel/internal/db"
)

const (
	statusRetentionMask uint16 = 0x3fff
	statusActive        uint16 = 1 << 14
	statusQueued        uint16 = 1 << 15
	maxCompactCount            = 1<<16 - 1
)

// memorySession is kept at 88 bytes because one instance lives for every
// active visitor. Counts spill into the owning shard only for the exceptional
// case of a session exceeding 65,535 pageviews or custom events.
type memorySession struct {
	visitorID int64
	sessionID int64
	startNS   int64
	lastNS    int64

	siteID       int32
	entry        int32
	exit         int32
	engagementMS int32
	source       int32
	medium       int32
	campaign     int32
	region       int32
	city         int32
	browser      int32
	os           int32
	counts       uint32

	day     uint16
	country uint16
	device  int16
	status  uint16
}

const memorySessionSize = unsafe.Sizeof(memorySession{})

var (
	_ [88 - memorySessionSize]byte
	_ [memorySessionSize - 88]byte
)

type sessionKey struct {
	visitorID int64
	siteID    int32
}

type overflowCounts struct {
	pageviews int32
	events    int32
}

type memoryShard struct {
	mu        sync.Mutex
	sessions  map[sessionKey]*memorySession
	dirty     []*memorySession
	overflow  map[*memorySession]*overflowCounts
	utmSource map[*memorySession]int32
	_         [24]byte // reduce false sharing between frequently locked shards
}

// Memory is a sharded in-memory Store. A session is owned by exactly one
// shard at a time, including its dirty-queue and overflow-count state.
type Memory struct {
	shards   []memoryShard
	clock    Clock
	activity activityIndex

	rngMu sync.Mutex
	rng   *mathrand.ChaCha8

	dirtyCursor atomic.Uint64
}

var _ Store = (*Memory)(nil)
var _ RealtimeStore = (*Memory)(nil)
var _ RichRealtimeStore = (*Memory)(nil)
var _ ActivityRecorder = (*Memory)(nil)

// NewMemory returns an empty store. Non-positive shard counts are normalized
// to one so configuration mistakes do not cause divide-by-zero panics.
func NewMemory(shards int, clk Clock) *Memory {
	if shards < 1 {
		shards = 1
	}
	var seed [32]byte
	if _, err := cryptorand.Read(seed[:]); err != nil {
		panic("session: secure random source unavailable")
	}
	m := &Memory{
		shards: make([]memoryShard, shards),
		clock:  clk,
		rng:    mathrand.NewChaCha8(seed),
	}
	m.activity.init()
	for i := range m.shards {
		m.shards[i].sessions = make(map[sessionKey]*memorySession)
	}
	return m
}

// Touch starts or extends a session. A previous-day visitor ID may move a
// still-active session to the current visitor's shard while preserving the
// original visitor ID, session ID, local day, and first-hit dimensions.
func (m *Memory) Touch(h Hit) Result {
	if h.TS.IsZero() && m.clock != nil {
		h.TS = m.clock.Now()
	}
	currentIndex := m.shardIndex(h.VisitorID)
	previousIndex := currentIndex
	if h.PrevVisitorID != 0 {
		previousIndex = m.shardIndex(h.PrevVisitorID)
	}

	first, second := currentIndex, previousIndex
	if first > second {
		first, second = second, first
	}
	m.shards[first].mu.Lock()
	if second != first {
		m.shards[second].mu.Lock()
	}
	defer func() {
		if second != first {
			m.shards[second].mu.Unlock()
		}
		m.shards[first].mu.Unlock()
	}()

	currentShard := &m.shards[currentIndex]
	currentKey := sessionKey{siteID: h.SiteID, visitorID: h.VisitorID}
	if s := currentShard.sessions[currentKey]; m.canContinue(s, h.TS) {
		m.apply(currentShard, s, h)
		m.markDirty(currentShard, s)
		return Result{SessionID: s.sessionID, VisitorID: s.visitorID}
	}

	if h.PrevVisitorID != 0 {
		previousShard := &m.shards[previousIndex]
		previousKey := sessionKey{siteID: h.SiteID, visitorID: h.PrevVisitorID}
		if s := previousShard.sessions[previousKey]; m.canContinue(s, h.TS) {
			if replaced := currentShard.sessions[currentKey]; replaced != nil && replaced != s {
				m.deactivate(currentShard, currentKey, replaced)
			}
			delete(previousShard.sessions, previousKey)
			if previousShard != currentShard {
				m.moveOwnership(previousShard, currentShard, s)
			}
			currentShard.sessions[currentKey] = s
			m.apply(currentShard, s, h)
			m.markDirty(currentShard, s)
			return Result{SessionID: s.sessionID, VisitorID: s.visitorID}
		}
	}

	if h.JoinOnly {
		return Result{}
	}
	if replaced := currentShard.sessions[currentKey]; replaced != nil {
		m.deactivate(currentShard, currentKey, replaced)
	}
	s := m.newSession(h)
	currentShard.sessions[currentKey] = s
	m.setUTMSource(currentShard, s, h.UTMSource)
	m.apply(currentShard, s, h)
	m.markDirty(currentShard, s)
	return Result{SessionID: s.sessionID, VisitorID: s.visitorID, New: true}
}

// Dirty returns at most max changed sessions and removes those entries from
// the dirty queues. A later Touch queues the session again.
func (m *Memory) Dirty(max int) []Snapshot {
	if max <= 0 {
		return nil
	}
	out := make([]Snapshot, 0, max)
	start := int(m.dirtyCursor.Add(1)-1) % len(m.shards)
	for offset := 0; offset < len(m.shards) && len(out) < max; offset++ {
		sh := &m.shards[(start+offset)%len(m.shards)]
		sh.mu.Lock()
		n := min(max-len(out), len(sh.dirty))
		for _, s := range sh.dirty[:n] {
			out = append(out, m.snapshot(sh, s))
			s.status &^= statusQueued
			if s.status&statusActive == 0 {
				delete(sh.overflow, s)
				delete(sh.utmSource, s)
			}
		}
		copy(sh.dirty, sh.dirty[n:])
		clear(sh.dirty[len(sh.dirty)-n:])
		sh.dirty = sh.dirty[:len(sh.dirty)-n]
		sh.mu.Unlock()
	}
	return out
}

// ActiveSince reports active visitors and unique exit paths ordered by active
// visitor count descending, then path ID ascending for deterministic ties.
func (m *Memory) ActiveSince(siteID int32, since time.Time) (int, []int32) {
	visitors, ranked, _ := m.ActiveBreakdownsSince(siteID, since)
	paths := make([]int32, len(ranked))
	for i := range ranked {
		paths[i] = ranked[i].ID
	}
	return visitors, paths
}

// ActiveBreakdownsSince reports active visitors plus ranked exit paths and
// first-touch sources. Ties are ordered by ID for deterministic responses.
func (m *Memory) ActiveBreakdownsSince(siteID int32, since time.Time) (int, []ActiveValue, []ActiveValue) {
	sinceNS := since.UnixNano()
	visitors := 0
	pathCounts := make(map[int32]int)
	sourceCounts := make(map[int32]int)
	for i := range m.shards {
		sh := &m.shards[i]
		sh.mu.Lock()
		for _, s := range sh.sessions {
			if s.siteID != siteID || s.lastNS < sinceNS {
				continue
			}
			visitors++
			if s.exit != 0 {
				pathCounts[s.exit]++
			}
			if s.source != 0 {
				sourceCounts[s.source]++
			}
		}
		sh.mu.Unlock()
	}

	return visitors, rankActiveValues(pathCounts), rankActiveValues(sourceCounts)
}

func rankActiveValues(counts map[int32]int) []ActiveValue {
	ranked := make([]ActiveValue, 0, len(counts))
	for id, count := range counts {
		ranked = append(ranked, ActiveValue{ID: id, Visitors: count})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Visitors != ranked[j].Visitors {
			return ranked[i].Visitors > ranked[j].Visitors
		}
		return ranked[i].ID < ranked[j].ID
	})
	return ranked
}

// Restore installs persisted open-session state without marking it dirty. If
// more than one snapshot addresses a visitor, the newest state wins.
func (m *Memory) Restore(snapshots []Snapshot) {
	for _, snapshot := range snapshots {
		index := m.shardIndex(snapshot.VisitorID)
		sh := &m.shards[index]
		key := sessionKey{siteID: snapshot.SiteID, visitorID: snapshot.VisitorID}
		sh.mu.Lock()
		if current := sh.sessions[key]; current != nil && current.lastNS >= snapshot.Last.UnixNano() {
			sh.mu.Unlock()
			continue
		} else if current != nil {
			m.deactivate(sh, key, current)
		}
		s := restoredSession(snapshot)
		sh.sessions[key] = s
		m.setUTMSource(sh, s, snapshot.UTMSource)
		m.setCounts(sh, s, snapshot.Pageviews, snapshot.Events)
		sh.mu.Unlock()
	}
}

// Expire drops sessions idle for longer than IdleTimeout. Dirty state remains
// available to the flusher until it is collected.
func (m *Memory) Expire(now time.Time) int {
	if now.IsZero() && m.clock != nil {
		now = m.clock.Now()
	}
	nowNS := now.UnixNano()
	expired := 0
	for i := range m.shards {
		sh := &m.shards[i]
		sh.mu.Lock()
		for key, s := range sh.sessions {
			if time.Duration(nowNS-s.lastNS) > IdleTimeout {
				m.deactivate(sh, key, s)
				expired++
			}
		}
		sh.mu.Unlock()
	}
	m.activity.prune(now.Add(-IdleTimeout).UnixNano())
	return expired
}

func (m *Memory) shardIndex(visitorID int64) int {
	return int(uint64(visitorID) % uint64(len(m.shards)))
}

func (m *Memory) canContinue(s *memorySession, at time.Time) bool {
	return s != nil && time.Duration(at.UnixNano()-s.lastNS) <= IdleTimeout
}

func (m *Memory) newSession(h Hit) *memorySession {
	return &memorySession{
		visitorID: h.VisitorID,
		sessionID: m.nextSessionID(),
		startNS:   h.TS.UnixNano(),
		lastNS:    h.TS.UnixNano(),
		siteID:    h.SiteID,
		source:    h.Source,
		medium:    h.Medium,
		campaign:  h.Campaign,
		region:    h.Region,
		city:      h.City,
		browser:   h.Browser,
		os:        h.OS,
		day:       encodeDay(h.LocalDay),
		country:   encodeCountry(h.Country),
		device:    h.Device,
		status:    uint16(h.Retention)&statusRetentionMask | statusActive,
	}
}

func restoredSession(snapshot Snapshot) *memorySession {
	return &memorySession{
		visitorID:    snapshot.VisitorID,
		sessionID:    snapshot.SessionID,
		startNS:      snapshot.Start.UnixNano(),
		lastNS:       snapshot.Last.UnixNano(),
		siteID:       snapshot.SiteID,
		entry:        snapshot.Entry,
		exit:         snapshot.Exit,
		engagementMS: snapshot.EngagementMS,
		source:       snapshot.Source,
		medium:       snapshot.Medium,
		campaign:     snapshot.Campaign,
		region:       snapshot.Region,
		city:         snapshot.City,
		browser:      snapshot.Browser,
		os:           snapshot.OS,
		day:          encodeDay(snapshot.LocalDay),
		country:      encodeCountry(snapshot.Country),
		device:       snapshot.Device,
		status:       uint16(snapshot.Retention)&statusRetentionMask | statusActive,
	}
}

func (m *Memory) apply(sh *memoryShard, s *memorySession, h Hit) {
	if timestamp := h.TS.UnixNano(); timestamp > s.lastNS {
		s.lastNS = timestamp
	}
	switch h.Kind {
	case 0:
		pageviews, _ := m.counts(sh, s)
		m.incrementPageviews(sh, s)
		if pageviews == 0 {
			s.entry = h.PathID
		}
		s.exit = h.PathID
	case 2:
		s.engagementMS += h.EngagementMS
	default:
		m.incrementEvents(sh, s)
	}
}

func (m *Memory) markDirty(sh *memoryShard, s *memorySession) {
	if s.status&statusQueued != 0 {
		return
	}
	s.status |= statusQueued
	sh.dirty = append(sh.dirty, s)
}

func (m *Memory) deactivate(sh *memoryShard, key sessionKey, s *memorySession) {
	delete(sh.sessions, key)
	s.status &^= statusActive
	if s.status&statusQueued == 0 {
		delete(sh.overflow, s)
		delete(sh.utmSource, s)
	}
}

func (m *Memory) moveOwnership(from, to *memoryShard, s *memorySession) {
	if s.status&statusQueued != 0 {
		for i, queued := range from.dirty {
			if queued != s {
				continue
			}
			copy(from.dirty[i:], from.dirty[i+1:])
			from.dirty[len(from.dirty)-1] = nil
			from.dirty = from.dirty[:len(from.dirty)-1]
			s.status &^= statusQueued
			break
		}
	}
	if overflow := from.overflow[s]; overflow != nil {
		if to.overflow == nil {
			to.overflow = make(map[*memorySession]*overflowCounts)
		}
		to.overflow[s] = overflow
		delete(from.overflow, s)
	}
	if utmSource := from.utmSource[s]; utmSource != 0 {
		m.setUTMSource(to, s, utmSource)
		delete(from.utmSource, s)
	}
}

func (m *Memory) snapshot(sh *memoryShard, s *memorySession) Snapshot {
	pageviews, events := m.counts(sh, s)
	return Snapshot{
		SiteID:       s.siteID,
		Retention:    db.RetentionClass(s.status & statusRetentionMask),
		SessionID:    s.sessionID,
		VisitorID:    s.visitorID,
		Start:        time.Unix(0, s.startNS).UTC(),
		Last:         time.Unix(0, s.lastNS).UTC(),
		LocalDay:     decodeDay(s.day),
		Entry:        s.entry,
		Exit:         s.exit,
		Pageviews:    pageviews,
		Events:       events,
		EngagementMS: s.engagementMS,
		Bounce:       pageviews == 1 && events == 0,
		Source:       s.source,
		UTMSource:    sh.utmSource[s],
		Medium:       s.medium,
		Campaign:     s.campaign,
		Country:      decodeCountry(s.country),
		Region:       s.region,
		City:         s.city,
		Browser:      s.browser,
		OS:           s.os,
		Device:       s.device,
	}
}

func (m *Memory) setUTMSource(sh *memoryShard, s *memorySession, source int32) {
	if source == 0 {
		return
	}
	if sh.utmSource == nil {
		sh.utmSource = make(map[*memorySession]int32)
	}
	sh.utmSource[s] = source
}

func (m *Memory) counts(sh *memoryShard, s *memorySession) (int32, int32) {
	if overflow := sh.overflow[s]; overflow != nil {
		return overflow.pageviews, overflow.events
	}
	return int32(s.counts & maxCompactCount), int32(s.counts >> 16)
}

func (m *Memory) setCounts(sh *memoryShard, s *memorySession, pageviews, events int32) {
	if pageviews >= 0 && pageviews <= maxCompactCount && events >= 0 && events <= maxCompactCount {
		s.counts = uint32(pageviews) | uint32(events)<<16
		return
	}
	if sh.overflow == nil {
		sh.overflow = make(map[*memorySession]*overflowCounts)
	}
	sh.overflow[s] = &overflowCounts{pageviews: pageviews, events: events}
}

func (m *Memory) incrementPageviews(sh *memoryShard, s *memorySession) {
	if overflow := sh.overflow[s]; overflow != nil {
		overflow.pageviews++
		return
	}
	pageviews := s.counts & maxCompactCount
	if pageviews < maxCompactCount {
		s.counts++
		return
	}
	m.setCounts(sh, s, int32(pageviews)+1, int32(s.counts>>16))
}

func (m *Memory) incrementEvents(sh *memoryShard, s *memorySession) {
	if overflow := sh.overflow[s]; overflow != nil {
		overflow.events++
		return
	}
	events := s.counts >> 16
	if events < maxCompactCount {
		s.counts += 1 << 16
		return
	}
	m.setCounts(sh, s, int32(s.counts&maxCompactCount), int32(events)+1)
}

func (m *Memory) nextSessionID() int64 {
	m.rngMu.Lock()
	defer m.rngMu.Unlock()
	for {
		if id := int64(m.rng.Uint64() >> 1); id != 0 {
			return id
		}
	}
}

func encodeDay(day time.Time) uint16 {
	return uint16(day.UTC().Unix() / int64((24 * time.Hour).Seconds()))
}

func decodeDay(day uint16) time.Time {
	return time.Unix(int64(day)*int64((24*time.Hour).Seconds()), 0).UTC()
}

func encodeCountry(country string) uint16 {
	var code [2]byte
	copy(code[:], country)
	return binary.BigEndian.Uint16(code[:])
}

func decodeCountry(country uint16) string {
	if country == 0 {
		return ""
	}
	code := [2]byte{byte(country >> 8), byte(country)}
	if code[1] == 0 {
		return string(code[:1])
	}
	return string(code[:])
}
