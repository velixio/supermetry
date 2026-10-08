package replay

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	indexBufferSize      = 4096
	indexShutdownTimeout = 10 * time.Second
)

type IndexUpdate struct {
	SiteID    int32
	SessionID int64
	Day       time.Time
	At        time.Time
	Bytes     int64
	Page      bool
	Trigger   string
	ProfileID *int64
}

type IndexWriter struct {
	pool       *pgxpool.Pool
	flushEvery time.Duration
	onDrop     func()
	ch         chan IndexUpdate

	stateMu   sync.RWMutex
	accepting bool
	started   atomic.Bool
}

func NewIndexWriter(pool *pgxpool.Pool, flushEvery time.Duration, onDrop func()) *IndexWriter {
	if flushEvery <= 0 {
		flushEvery = time.Second
	}
	if onDrop == nil {
		onDrop = func() {}
	}
	return &IndexWriter{
		pool: pool, flushEvery: flushEvery, onDrop: onDrop,
		ch: make(chan IndexUpdate, indexBufferSize), accepting: true,
	}
}

func (w *IndexWriter) Add(update IndexUpdate) {
	w.stateMu.RLock()
	if !w.accepting {
		w.stateMu.RUnlock()
		w.onDrop()
		return
	}
	select {
	case w.ch <- update:
		w.stateMu.RUnlock()
	default:
		w.stateMu.RUnlock()
		w.onDrop()
	}
}

func (w *IndexWriter) Run(ctx context.Context) error {
	if !w.started.CompareAndSwap(false, true) {
		return errors.New("replay index writer: Run called more than once")
	}
	defer w.stopAccepting()
	ticker := time.NewTicker(w.flushEvery)
	defer ticker.Stop()
	pending := make(map[indexKey]indexRow)
	started := make(map[indexKey]sessionStart)

	for {
		select {
		case update := <-w.ch:
			coalesceIndexUpdate(pending, started, update)
		case <-ticker.C:
			w.drain(pending, started)
			if err := w.flush(ctx, pending); err != nil {
				if ctx.Err() != nil {
					return w.shutdown(pending, started)
				}
				return err
			}
			clear(pending)
		case <-ctx.Done():
			return w.shutdown(pending, started)
		}
	}
}

type indexKey struct {
	siteID    int32
	sessionID int64
}

type sessionStart struct {
	at  time.Time
	day time.Time
}

type indexRow struct {
	key        indexKey
	startedAt  time.Time
	lastSeenAt time.Time
	day        time.Time
	bytes      int64
	pages      int64
	trigger    string
	profileID  *int64
}

func coalesceIndexUpdate(pending map[indexKey]indexRow, started map[indexKey]sessionStart, update IndexUpdate) {
	key := indexKey{siteID: update.SiteID, sessionID: update.SessionID}
	start, ok := started[key]
	if !ok {
		start = sessionStart{at: update.At, day: update.Day}
		started[key] = start
	}
	row, ok := pending[key]
	if !ok {
		row = indexRow{key: key, startedAt: start.at, lastSeenAt: update.At, day: start.day}
	}
	if update.At.After(row.lastSeenAt) {
		row.lastSeenAt = update.At
	}
	row.bytes += update.Bytes
	if update.Page {
		row.pages++
	}
	if update.Trigger != "" {
		row.trigger = update.Trigger
	}
	if update.ProfileID != nil {
		profileID := *update.ProfileID
		row.profileID = &profileID
	}
	pending[key] = row
}

func (w *IndexWriter) drain(pending map[indexKey]indexRow, started map[indexKey]sessionStart) {
	for {
		select {
		case update := <-w.ch:
			coalesceIndexUpdate(pending, started, update)
		default:
			return
		}
	}
}

func (w *IndexWriter) flush(ctx context.Context, pending map[indexKey]indexRow) error {
	if len(pending) == 0 {
		return nil
	}
	if w.pool == nil {
		return errors.New("replay index writer has no database pool")
	}
	keys := make([]indexKey, 0, len(pending))
	for key := range pending {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].siteID != keys[j].siteID {
			return keys[i].siteID < keys[j].siteID
		}
		return keys[i].sessionID < keys[j].sessionID
	})

	var query strings.Builder
	query.WriteString(`INSERT INTO replay_sessions
(site_id, session_id, started_at, last_seen_at, day, bytes, pages, trigger, profile_id, blob_key) VALUES `)
	args := make([]any, 0, len(keys)*10)
	for rowIndex, key := range keys {
		if rowIndex != 0 {
			query.WriteByte(',')
		}
		query.WriteByte('(')
		for column := 0; column < 10; column++ {
			if column != 0 {
				query.WriteByte(',')
			}
			fmt.Fprintf(&query, "$%d", rowIndex*10+column+1)
		}
		query.WriteByte(')')
		row := pending[key]
		var trigger any
		if row.trigger != "" {
			trigger = row.trigger
		}
		args = append(args,
			key.siteID, key.sessionID, row.startedAt, row.lastSeenAt, row.day, row.bytes, row.pages,
			trigger, row.profileID, SessionKey(key.siteID, row.day, key.sessionID),
		)
	}
	query.WriteString(` ON CONFLICT (site_id, started_at, session_id) DO UPDATE SET
last_seen_at = GREATEST(replay_sessions.last_seen_at, EXCLUDED.last_seen_at),
bytes = replay_sessions.bytes + EXCLUDED.bytes,
pages = replay_sessions.pages + EXCLUDED.pages,
trigger = COALESCE(EXCLUDED.trigger, replay_sessions.trigger),
profile_id = COALESCE(EXCLUDED.profile_id, replay_sessions.profile_id)`)
	if _, err := w.pool.Exec(ctx, query.String(), args...); err != nil {
		return fmt.Errorf("upsert replay index batch: %w", err)
	}
	return nil
}

func (w *IndexWriter) shutdown(pending map[indexKey]indexRow, started map[indexKey]sessionStart) error {
	w.stopAccepting()
	w.drain(pending, started)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), indexShutdownTimeout)
	defer cancel()
	if err := w.flush(shutdownCtx, pending); err != nil {
		return fmt.Errorf("flush replay index at shutdown: %w", err)
	}
	return nil
}

func (w *IndexWriter) stopAccepting() {
	w.stateMu.Lock()
	w.accepting = false
	w.stateMu.Unlock()
}
