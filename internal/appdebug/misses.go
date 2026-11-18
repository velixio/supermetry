// Package appdebug records the debug files events needed and projects lack,
// and serves the Symbols settings reads and deletes.
package appdebug

import (
	"context"
	"log/slog"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/appingest"
	"supermetry/internal/debugfile"
)

// DefaultMaxKeys bounds the keys held between flushes.
const DefaultMaxKeys = 10000

// Counter receives named counts (the ingest Stats satisfies it).
type Counter interface {
	Add(category string, n uint64)
}

type missKey struct {
	site int32
	kind string
	id   string
}

type missVal struct {
	file, arch  string
	first, last time.Time
	events      int64
}

// MissRecorder aggregates missing debug images in memory and upserts them in
// batches, so a crash loop of one app costs one row, not one write per event.
// It implements sentryingest.MissSink.
type MissRecorder struct {
	pool    *pgxpool.Pool
	max     int
	every   time.Duration
	stats   Counter
	mu      sync.Mutex
	pending map[missKey]*missVal
	dropped uint64
	started bool
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

// NewMissRecorder returns a recorder; Start launches the periodic flush. A
// non-positive max means DefaultMaxKeys, a non-positive flushEvery 10 s.
func NewMissRecorder(pool *pgxpool.Pool, flushEvery time.Duration, max int) *MissRecorder {
	if max <= 0 {
		max = DefaultMaxKeys
	}
	if flushEvery <= 0 {
		flushEvery = 10 * time.Second
	}
	return &MissRecorder{pool: pool, max: max, every: flushEvery, pending: map[missKey]*missVal{}, stop: make(chan struct{}), done: make(chan struct{})}
}

// SetStats makes overflow drops visible as symbol_miss_dropped.
func (r *MissRecorder) SetStats(c Counter) { r.stats = c }

// Dropped is the number of misses discarded because the key cap was reached.
func (r *MissRecorder) Dropped() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dropped
}

var systemPrefixes = []string{"/system/", "/usr/lib/", "/apex/", "/vendor/", "/lib/", "/lib64/", "/usr/lib64/"}

// isSystemImage reports images users never upload symbols for: the OS's own
// libraries.
func isSystemImage(codeFile string) bool {
	f := strings.ToLower(strings.ReplaceAll(codeFile, `\`, "/"))
	for _, p := range systemPrefixes {
		if strings.HasPrefix(f, p) {
			return true
		}
	}
	base := f[strings.LastIndex(f, "/")+1:]
	return base == "libc.so" || strings.HasPrefix(base, "libsystem_")
}

func baseName(codeFile string) string {
	s := appingest.ScrubFrames(strings.ReplaceAll(codeFile, `\`, "/"))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	b := path.Base(s)
	if b == "." || b == "/" {
		return ""
	}
	if len(b) > 200 {
		b = b[:200]
	}
	return strings.ToValidUTF8(b, "")
}

// Missing records that an event of the site needed a debug file of the kind
// ("proguard", "macho", "elf") that no upload provides.
func (r *MissRecorder) Missing(siteID int32, kind string, im appingest.DebugImage, at time.Time) {
	raw := im.DebugID
	if kind == "proguard" {
		raw = im.UUID
	}
	id := debugfile.NormalizeDebugID(raw)
	if id == "" {
		id = strings.ToLower(strings.TrimSpace(raw))
	}
	if id == "" || len(id) > 64 || len(kind) > 32 || isSystemImage(im.CodeFile) {
		return
	}
	k := missKey{siteID, kind, id}
	r.mu.Lock()
	defer r.mu.Unlock()
	v := r.pending[k]
	if v == nil {
		if len(r.pending) >= r.max {
			r.dropped++
			if r.stats != nil {
				r.stats.Add("symbol_miss_dropped", 1)
			}
			return
		}
		arch := im.Arch
		if len(arch) > 32 {
			arch = arch[:32]
		}
		v = &missVal{file: baseName(im.CodeFile), arch: arch, first: at, last: at}
		r.pending[k] = v
	}
	v.events++
	if at.Before(v.first) {
		v.first = at
	}
	if at.After(v.last) {
		v.last = at
	}
}

// Start runs the periodic flush until Close.
func (r *MissRecorder) Start() {
	r.mu.Lock()
	r.started = true
	r.mu.Unlock()
	go func() {
		defer close(r.done)
		t := time.NewTicker(r.every)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				if err := r.Flush(ctx); err != nil {
					slog.Error("appdebug: flush missing debug files", "err", err)
				}
				cancel()
			case <-r.stop:
				return
			}
		}
	}()
}

// Close stops the flush loop (if started) and writes what is left.
func (r *MissRecorder) Close() {
	r.once.Do(func() {
		r.mu.Lock()
		started := r.started
		r.mu.Unlock()
		close(r.stop)
		if started {
			<-r.done
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := r.Flush(ctx); err != nil {
			slog.Error("appdebug: final flush of missing debug files", "err", err)
		}
	})
}

// Flush upserts everything recorded so far. A row for a file that has since
// been uploaded is not written (the upload deleted its row; a late flush must
// not bring the banner back). On error the batch is kept for the next flush.
func (r *MissRecorder) Flush(ctx context.Context) error {
	r.mu.Lock()
	batch := r.pending
	r.pending = map[missKey]*missVal{}
	r.mu.Unlock()
	if len(batch) == 0 || r.pool == nil {
		return nil
	}
	keys := make([]missKey, 0, len(batch))
	for k := range batch {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.site != b.site {
			return a.site < b.site
		}
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		return a.id < b.id
	})
	const q = `
insert into app_missing_debug_files as t (site_id, kind, debug_id, code_file, arch, first_seen, last_seen, events)
select $1::integer, $2::text, $3::text, $4::text, $5::text, $6::timestamptz, $7::timestamptz, $8::bigint
where not exists (
  select 1 from app_debug_files f join sites s on s.id = $1::integer
  where f.org_id = s.org_id and f.kind = $2::text and f.debug_id = $3::text and f.cache_state = 'ok' and (f.site_id is null or f.site_id = $1::integer))
on conflict (site_id, kind, debug_id) do update set
  events = t.events + excluded.events,
  first_seen = least(t.first_seen, excluded.first_seen),
  last_seen = greatest(t.last_seen, excluded.last_seen),
  code_file = case when t.code_file = '' then excluded.code_file else t.code_file end,
  arch = case when t.arch = '' then excluded.arch else t.arch end`
	b := &pgx.Batch{}
	for _, k := range keys {
		v := batch[k]
		b.Queue(q, k.site, k.kind, k.id, v.file, v.arch, v.first, v.last, v.events)
	}
	res := r.pool.SendBatch(ctx, b)
	var firstErr error
	for range keys {
		if _, err := res.Exec(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := res.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if firstErr != nil {
		r.mu.Lock()
		for k, v := range batch { // keep for the next flush, merging newer misses
			if cur := r.pending[k]; cur != nil {
				cur.events += v.events
				if v.first.Before(cur.first) {
					cur.first = v.first
				}
			} else if len(r.pending) < r.max {
				r.pending[k] = v
			}
		}
		r.mu.Unlock()
	}
	return firstErr
}
