// Package dropstats records bounded per-site traffic drops without delaying ingest.
package dropstats

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultMaxSites    = 10_000
	defaultFlushEvery  = 30 * time.Second
	maxDaysPerSite     = 3
	maxBotRules        = 20
	maxRuleBytes       = 160
	maxBatchStatements = 500
	botRuleFlushLock   = int64(0x5355504552445250)
)

// Reason is a stable persisted drop category.
type Reason uint8

const (
	BotUserAgent Reason = iota
	BotHeadless
	BotSpamReferrer
	RateLimitedIP
	RateLimitedSite
	RateLimitedGlobal
	PrivacySignal
	DimensionOverflow
	HostNotAllowed
	ClockSkew
	reasonCount
)

var reasonNames = [...]string{
	"bot_user_agent", "bot_headless", "bot_spam_referrer",
	"rate_limited_ip", "rate_limited_site", "rate_limited_global",
	"privacy_signal", "dimension_overflow", "host_not_allowed", "clock_skew",
}

func (r Reason) String() string {
	if r >= reasonCount {
		return ""
	}
	return reasonNames[r]
}

// Reasons returns all persisted reasons in stable order.
func Reasons() []Reason {
	result := make([]Reason, reasonCount)
	for reason := range reasonCount {
		result[reason] = reason
	}
	return result
}

// Options bounds memory and controls periodic persistence.
type Options struct {
	MaxSites   int
	FlushEvery time.Duration
}

type ruleCounter struct {
	rule  string
	count uint64
}

type dayCounters struct {
	day       atomic.Int64
	counts    [reasonCount]atomic.Uint64
	ruleMu    sync.Mutex
	rules     [maxBotRules]ruleCounter
	used      uint8
	candidate ruleCounter
}

type siteBuffer struct {
	writers atomic.Int64
	days    [maxDaysPerSite]dayCounters
}

type siteCounters struct {
	siteID  int32
	current atomic.Pointer[siteBuffer]
}

// Recorder keeps bounded deltas in memory and persists them periodically.
type Recorder struct {
	pool       *pgxpool.Pool
	maxSites   int64
	flushEvery time.Duration
	tracked    atomic.Int64
	sites      []atomic.Pointer[siteCounters]
	flushMu    sync.Mutex
}

// New constructs a recorder. A nil pool is useful for bounded hot-path tests.
func New(pool *pgxpool.Pool, options Options) *Recorder {
	if options.MaxSites <= 0 {
		options.MaxSites = defaultMaxSites
	}
	if options.FlushEvery <= 0 {
		options.FlushEvery = defaultFlushEvery
	}
	return &Recorder{
		pool: pool, maxSites: int64(options.MaxSites), flushEvery: options.FlushEvery,
		sites: make([]atomic.Pointer[siteCounters], options.MaxSites),
	}
}

// Record increments a counter without waiting behind a flush or another
// ingest request. Bot-rule detail is best-effort when its small bounded table
// is contended; the per-reason count is always atomic.
func (r *Recorder) Record(siteID int32, day time.Time, reason Reason, botRule string) bool {
	if r == nil || siteID <= 0 || reason >= reasonCount {
		return false
	}
	site := r.site(siteID)
	if site == nil {
		return false
	}
	key := dayKey(day)
	for {
		buffer := site.current.Load()
		buffer.writers.Add(1)
		if site.current.Load() != buffer {
			buffer.writers.Add(-1)
			continue
		}
		counters := buffer.forDay(key)
		if counters == nil {
			buffer.writers.Add(-1)
			return false
		}
		counters.counts[reason].Add(1)
		if reason <= BotSpamReferrer && counters.ruleMu.TryLock() {
			counters.recordRule(normalizeRule(botRule, reason), 1)
			counters.ruleMu.Unlock()
		}
		buffer.writers.Add(-1)
		return true
	}
}

func (r *Recorder) site(siteID int32) *siteCounters {
	start := int((uint32(siteID) * 2654435761) % uint32(len(r.sites)))
	for offset := range len(r.sites) {
		slot := &r.sites[(start+offset)%len(r.sites)]
		if existing := slot.Load(); existing != nil {
			if existing.siteID == siteID {
				return existing
			}
			continue
		}
		if !r.reserveSite() {
			return nil
		}
		candidate := &siteCounters{siteID: siteID}
		candidate.current.Store(&siteBuffer{})
		if slot.CompareAndSwap(nil, candidate) {
			return candidate
		}
		r.tracked.Add(-1)
		if existing := slot.Load(); existing != nil && existing.siteID == siteID {
			return existing
		}
	}
	return nil
}

func (r *Recorder) existingSite(siteID int32) *siteCounters {
	start := int((uint32(siteID) * 2654435761) % uint32(len(r.sites)))
	for offset := range len(r.sites) {
		existing := r.sites[(start+offset)%len(r.sites)].Load()
		if existing == nil {
			return nil
		}
		if existing.siteID == siteID {
			return existing
		}
	}
	return nil
}

func (r *Recorder) reserveSite() bool {
	for {
		current := r.tracked.Load()
		if current >= r.maxSites {
			return false
		}
		if r.tracked.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func (b *siteBuffer) forDay(key int64) *dayCounters {
	for attempts := 0; attempts < maxDaysPerSite+1; attempts++ {
		firstEmpty := -1
		for index := range b.days {
			current := b.days[index].day.Load()
			if current == key {
				return &b.days[index]
			}
			if current == 0 && firstEmpty < 0 {
				firstEmpty = index
			}
		}
		if firstEmpty < 0 {
			return nil
		}
		if b.days[firstEmpty].day.CompareAndSwap(0, key) {
			return &b.days[firstEmpty]
		}
	}
	return nil
}

func (d *dayCounters) recordRule(rule string, count uint64) {
	for index := range int(d.used) {
		if d.rules[index].rule == rule {
			d.rules[index].count += count
			return
		}
	}
	if d.used < maxBotRules-1 {
		index := d.used
		d.used++
		d.rules[index] = ruleCounter{rule: rule, count: count}
		return
	}
	overflow := &d.rules[maxBotRules-1]
	overflow.rule = "other"
	overflow.count += count
	if rule == "other" {
		return
	}
	if d.candidate.rule == rule {
		d.candidate.count += count
	} else if d.candidate.count > count {
		d.candidate.count -= count
	} else {
		d.candidate = ruleCounter{rule: rule, count: count - d.candidate.count}
	}
	minIndex := 0
	for index := 1; index < int(d.used); index++ {
		if d.rules[index].count < d.rules[minIndex].count {
			minIndex = index
		}
	}
	if d.candidate.count > d.rules[minIndex].count {
		overflow.count -= d.candidate.count
		overflow.count += d.rules[minIndex].count
		d.rules[minIndex] = d.candidate
		d.candidate = ruleCounter{}
	}
}

func normalizeRule(rule string, reason Reason) string {
	if rule == "" {
		rule = reason.String()
	}
	if len(rule) > maxRuleBytes {
		rule = rule[:maxRuleBytes]
	}
	return rule
}

func dateOnly(day time.Time) time.Time {
	year, month, date := day.Date()
	return time.Date(year, month, date, 0, 0, 0, 0, time.UTC)
}

func dayKey(day time.Time) int64 {
	year, month, date := day.Date()
	return int64(year*10_000 + int(month)*100 + date)
}

func keyDay(key int64) time.Time {
	year := int(key / 10_000)
	month := time.Month((key / 100) % 100)
	date := int(key % 100)
	return time.Date(year, month, date, 0, 0, 0, 0, time.UTC)
}

// TrackedSites returns the number of site slots reserved by this process.
func (r *Recorder) TrackedSites() int { return int(r.tracked.Load()) }

type dropDelta struct {
	siteID int32
	day    time.Time
	reason Reason
	count  uint64
}

type ruleDelta struct {
	siteID int32
	day    time.Time
	rule   string
	count  uint64
}

// Flush atomically detaches current deltas and upserts them in one transaction.
func (r *Recorder) Flush(ctx context.Context) error {
	if r == nil || r.pool == nil {
		return nil
	}
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	drops, rules := r.take()
	if len(drops) == 0 && len(rules) == 0 {
		return nil
	}
	if err := r.persist(ctx, drops, rules); err != nil {
		r.restore(drops, rules)
		return err
	}
	return nil
}

func (r *Recorder) take() ([]dropDelta, []ruleDelta) {
	var drops []dropDelta
	var rules []ruleDelta
	for index := range r.sites {
		site := r.sites[index].Load()
		if site == nil {
			continue
		}
		old := site.current.Swap(&siteBuffer{})
		for old.writers.Load() != 0 {
			runtime.Gosched()
		}
		for dayIndex := range old.days {
			day := &old.days[dayIndex]
			key := day.day.Load()
			if key == 0 {
				continue
			}
			date := keyDay(key)
			for reason := range reasonCount {
				if count := day.counts[reason].Load(); count != 0 {
					drops = append(drops, dropDelta{siteID: site.siteID, day: date, reason: reason, count: count})
				}
			}
			for ruleIndex := range int(day.used) + 1 {
				rule := day.rules[ruleIndex]
				if rule.count != 0 {
					rules = append(rules, ruleDelta{siteID: site.siteID, day: date, rule: rule.rule, count: rule.count})
				}
			}
		}
	}
	return drops, rules
}

func (r *Recorder) persist(ctx context.Context, drops []dropDelta, rules []ruleDelta) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin drop stats flush: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	drops, rules, err = keepExistingSites(ctx, tx, drops, rules)
	if err != nil {
		return err
	}
	sort.Slice(drops, func(i, j int) bool {
		if drops[i].siteID != drops[j].siteID {
			return drops[i].siteID < drops[j].siteID
		}
		if !drops[i].day.Equal(drops[j].day) {
			return drops[i].day.Before(drops[j].day)
		}
		return drops[i].reason < drops[j].reason
	})
	for start := 0; start < len(drops); start += maxBatchStatements {
		end := min(start+maxBatchStatements, len(drops))
		batch := &pgx.Batch{}
		for _, delta := range drops[start:end] {
			batch.Queue(`insert into site_drop_daily(site_id,day,reason,count) values($1,$2,$3,$4)
on conflict(site_id,day,reason) do update set count=site_drop_daily.count+excluded.count`, delta.siteID, delta.day, delta.reason.String(), delta.count)
		}
		if err := sendBatch(ctx, tx, batch); err != nil {
			return fmt.Errorf("write drop stats batch: %w", err)
		}
	}
	if len(rules) != 0 {
		// Serialize rule flushes across instances so the persisted 19-name plus
		// overflow bound survives restarts and concurrent processes.
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, botRuleFlushLock); err != nil {
			return fmt.Errorf("lock bot rule flush: %w", err)
		}
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].siteID != rules[j].siteID {
			return rules[i].siteID < rules[j].siteID
		}
		if !rules[i].day.Equal(rules[j].day) {
			return rules[i].day.Before(rules[j].day)
		}
		return rules[i].rule < rules[j].rule
	})
	for start := 0; start < len(rules); start += maxBatchStatements {
		end := min(start+maxBatchStatements, len(rules))
		batch := &pgx.Batch{}
		for _, delta := range rules[start:end] {
			batch.Queue(`with state as materialized (
  select
    coalesce(bool_or(rule=$3),false) as known,
    count(*) filter(where rule<>'other') as named_count,
    (array_agg(rule order by count,rule) filter(where rule<>'other'))[1] as min_rule,
    min(count) filter(where rule<>'other') as min_count
  from site_bot_rule_daily where site_id=$1 and day=$2
), decision as (
  select *, $3<>'other' and not known and named_count>=19 and $4>coalesce(min_count,0) as promote
  from state
), moved as (
  delete from site_bot_rule_daily b using decision d
  where d.promote and b.site_id=$1 and b.day=$2 and b.rule=d.min_rule
  returning b.count
), overflow as (
  insert into site_bot_rule_daily(site_id,day,rule,count)
  select $1,$2,'other',sum(count) from moved having count(*)>0
  on conflict(site_id,day,rule) do update set count=site_bot_rule_daily.count+excluded.count
), chosen as (
  select case when $3='other' or known or named_count<19 or promote then $3 else 'other' end as rule
  from decision
)
insert into site_bot_rule_daily(site_id,day,rule,count)
select $1,$2,rule,$4 from chosen
on conflict(site_id,day,rule) do update set count=site_bot_rule_daily.count+excluded.count`, delta.siteID, delta.day, delta.rule, delta.count)
		}
		if err := sendBatch(ctx, tx, batch); err != nil {
			return fmt.Errorf("write bot rule stats batch: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit drop stats flush: %w", err)
	}
	return nil
}

// keepExistingSites drops deltas for sites that no longer exist (deleted while
// their counters were buffered, or never stored in the sites table). Their rows
// would violate the foreign keys and, because failed deltas are retried, one
// such site would otherwise stop every other site's counters from persisting.
func keepExistingSites(ctx context.Context, tx pgx.Tx, drops []dropDelta, rules []ruleDelta) ([]dropDelta, []ruleDelta, error) {
	seen := make(map[int32]struct{})
	ids := make([]int32, 0)
	for _, delta := range drops {
		if _, ok := seen[delta.siteID]; !ok {
			seen[delta.siteID] = struct{}{}
			ids = append(ids, delta.siteID)
		}
	}
	for _, delta := range rules {
		if _, ok := seen[delta.siteID]; !ok {
			seen[delta.siteID] = struct{}{}
			ids = append(ids, delta.siteID)
		}
	}
	if len(ids) == 0 {
		return drops, rules, nil
	}
	rows, err := tx.Query(ctx, `select id from sites where id = any($1)`, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("check drop stats sites: %w", err)
	}
	existing := make(map[int32]struct{}, len(ids))
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("read drop stats sites: %w", err)
		}
		existing[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("check drop stats sites: %w", err)
	}
	keptDrops := drops[:0:0]
	for _, delta := range drops {
		if _, ok := existing[delta.siteID]; ok {
			keptDrops = append(keptDrops, delta)
		}
	}
	keptRules := rules[:0:0]
	for _, delta := range rules {
		if _, ok := existing[delta.siteID]; ok {
			keptRules = append(keptRules, delta)
		}
	}
	return keptDrops, keptRules, nil
}

func sendBatch(ctx context.Context, tx pgx.Tx, batch *pgx.Batch) error {
	return tx.SendBatch(ctx, batch).Close()
}

func (r *Recorder) restore(drops []dropDelta, rules []ruleDelta) {
	for _, delta := range drops {
		r.restoreDrop(delta)
	}
	for _, delta := range rules {
		r.restoreRule(delta)
	}
}

func (r *Recorder) restoreDrop(delta dropDelta) {
	site := r.existingSite(delta.siteID)
	if site == nil {
		return
	}
	for {
		buffer := site.current.Load()
		buffer.writers.Add(1)
		if site.current.Load() != buffer {
			buffer.writers.Add(-1)
			continue
		}
		if day := buffer.forDay(dayKey(delta.day)); day != nil {
			day.counts[delta.reason].Add(delta.count)
		}
		buffer.writers.Add(-1)
		return
	}
}

func (r *Recorder) restoreRule(delta ruleDelta) {
	site := r.existingSite(delta.siteID)
	if site == nil {
		return
	}
	for {
		buffer := site.current.Load()
		buffer.writers.Add(1)
		if site.current.Load() != buffer {
			buffer.writers.Add(-1)
			continue
		}
		if day := buffer.forDay(dayKey(delta.day)); day != nil {
			day.ruleMu.Lock()
			day.recordRule(delta.rule, delta.count)
			day.ruleMu.Unlock()
		}
		buffer.writers.Add(-1)
		return
	}
}

// Run flushes periodically and once more when the context is cancelled.
func (r *Recorder) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := r.Flush(flushCtx)
			cancel()
			return err
		case <-ticker.C:
			if err := r.Flush(ctx); err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
		}
	}
}

// SiteDay identifies a site's current local day.
type SiteDay struct {
	SiteID int32
	Today  time.Time
}

// BotRuleCount is a bounded persisted bot-rule total.
type BotRuleCount struct {
	Rule  string `json:"rule"`
	Count int64  `json:"count"`
}

// SiteSummary contains today's and the previous seven complete days' counts.
type SiteSummary struct {
	SiteID      int32
	Today       [reasonCount]int64
	Previous7   [reasonCount]int64
	TopBotRules []BotRuleCount
}

// Read loads summaries for exactly the requested sites.
func (r *Recorder) Read(ctx context.Context, sites []SiteDay) ([]SiteSummary, error) {
	result := make([]SiteSummary, len(sites))
	if len(sites) == 0 || r == nil || r.pool == nil {
		return result, nil
	}
	ids := make([]int32, len(sites))
	days := make([]time.Time, len(sites))
	byID := make(map[int32]int, len(sites))
	for index, site := range sites {
		ids[index], days[index] = site.SiteID, dateOnly(site.Today)
		result[index].SiteID = site.SiteID
		byID[site.SiteID] = index
	}
	rows, err := r.pool.Query(ctx, `with requested(site_id,today) as (select * from unnest($1::integer[],$2::date[]))
select d.site_id,d.day,d.reason,d.count,r.today
from requested r join site_drop_daily d on d.site_id=r.site_id and d.day between r.today-7 and r.today`, ids, days)
	if err != nil {
		return nil, fmt.Errorf("read site drop stats: %w", err)
	}
	for rows.Next() {
		var siteID int32
		var day, today time.Time
		var name string
		var count int64
		if err := rows.Scan(&siteID, &day, &name, &count, &today); err != nil {
			rows.Close()
			return nil, err
		}
		reason, ok := parseReason(name)
		if !ok {
			continue
		}
		if day.Equal(today) {
			result[byID[siteID]].Today[reason] += count
		} else {
			result[byID[siteID]].Previous7[reason] += count
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	ruleRows, err := r.pool.Query(ctx, `with requested(site_id,today) as (select * from unnest($1::integer[],$2::date[]))
select b.site_id,b.rule,b.count from requested r join site_bot_rule_daily b on b.site_id=r.site_id and b.day=r.today
order by b.site_id,b.count desc,b.rule limit $3`, ids, days, len(sites)*maxBotRules)
	if err != nil {
		return nil, fmt.Errorf("read site bot rules: %w", err)
	}
	defer ruleRows.Close()
	for ruleRows.Next() {
		var siteID int32
		var rule string
		var count int64
		if err := ruleRows.Scan(&siteID, &rule, &count); err != nil {
			return nil, err
		}
		index := byID[siteID]
		if len(result[index].TopBotRules) < maxBotRules {
			result[index].TopBotRules = append(result[index].TopBotRules, BotRuleCount{Rule: rule, Count: count})
		}
	}
	return result, ruleRows.Err()
}

func parseReason(name string) (Reason, bool) {
	for reason, candidate := range reasonNames {
		if candidate == name {
			return Reason(reason), true
		}
	}
	return 0, false
}

// Spike reports whether today is at least 100 and at least five times the
// average of the previous seven complete local days.
func Spike(today, previous7 int64) bool {
	return today >= 100 && today*7 >= previous7*5
}
