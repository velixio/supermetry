package appingest

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/apprelease"
	"supermetry/internal/db"
	"supermetry/internal/health"
)

const (
	defaultBatchSize  = 500
	defaultFlushEvery = time.Second
	shutdownTimeout   = 15 * time.Second
	defaultRetryDelay = time.Second
	fkViolation       = "23503"
)

// WriterConfig tunes a Writer. Zero values use the defaults.
type WriterConfig struct {
	BatchSize  int
	FlushEvery time.Duration
}

// Writer drains an Intake's queue into Postgres: it creates missing hash
// secrets, upserts issues (with regression detection) and copies the events.
// It is the only goroutine that touches the database for app events.
type Writer struct {
	pool       *pgxpool.Pool
	in         *Intake
	batchSize  int
	flushEvery time.Duration

	// runTx runs fn in a transaction that rolls back on error. Tests replace it.
	runTx      func(ctx context.Context, fn func(pgx.Tx) error) error
	retryDelay time.Duration

	mu         sync.Mutex
	secrets    map[int32][]byte
	partitions map[time.Time]struct{}
}

// NewWriter builds a writer for in's queue.
func NewWriter(pool *pgxpool.Pool, in *Intake, cfg WriterConfig) *Writer {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultBatchSize
	}
	if cfg.FlushEvery <= 0 {
		cfg.FlushEvery = defaultFlushEvery
	}
	w := &Writer{
		pool: pool, in: in, batchSize: cfg.BatchSize, flushEvery: cfg.FlushEvery, retryDelay: defaultRetryDelay,
		secrets: map[int32][]byte{}, partitions: map[time.Time]struct{}{},
	}
	w.runTx = func(ctx context.Context, fn func(pgx.Tx) error) error { return pgx.BeginFunc(ctx, w.pool, fn) }
	return w
}

// errCommitUnknown marks a failure after the batch ran to the end inside its
// transaction: the commit may or may not have happened, so replaying could
// count the batch twice.
var errCommitUnknown = errors.New("commit outcome unknown")

// flushRetry writes a batch. When the transaction rolled back it waits one
// retry delay and writes the same batch once more (the rollback leaves nothing
// behind, so the issue upsert cannot count it twice); a failed retry drops the
// batch and counts it. A failure whose commit outcome is unknown is dropped
// without a retry.
func (w *Writer) flushRetry(ctx context.Context, batch []Queued) error {
	err := w.flush(ctx, batch)
	if err != nil && !errors.Is(err, errCommitUnknown) {
		slog.Warn("app event write failed, retrying once", "events", len(batch), "error", err)
		timer := time.NewTimer(w.retryDelay)
		select {
		case <-timer.C:
			err = w.flush(ctx, batch)
		case <-ctx.Done():
			timer.Stop()
		}
	}
	if err != nil {
		health.AppIngestWriteFailed.Add(uint64(len(batch)))
		slog.Error("app event write failed", "events", len(batch), "error", err)
	}
	return err
}

// Run flushes on batch size and on an interval until ctx ends, then drains the
// queue and flushes what is left before returning.
func (w *Writer) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.flushEvery)
	defer ticker.Stop()
	batch := make([]Queued, 0, w.batchSize)
	flush := func(c context.Context) {
		if len(batch) == 0 {
			return
		}
		_ = w.flushRetry(c, batch) // failures are counted and logged there
		batch = batch[:0]
	}
	for {
		select {
		case q := <-w.in.ch:
			batch = append(batch, q)
			if len(batch) >= w.batchSize {
				flush(ctx)
			}
		case <-ticker.C:
			flush(ctx)
		case <-ctx.Done():
			w.in.close()
			final, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			for {
				select {
				case q := <-w.in.ch:
					batch = append(batch, q)
					if len(batch) >= w.batchSize {
						flush(final)
					}
					continue
				default:
				}
				break
			}
			flush(final)
			return nil
		}
	}
}

type issueGroup struct {
	siteID                     int32
	fingerprint, kind, errType string
	title                      string
	firstSeen, lastSeen        time.Time
	firstRelease, lastRelease  string
	count                      int64
	releases                   map[string]struct{}
	id                         int64
	status                     string
	resolvedIn                 *string
	resolvedNext               bool
	level, culprit             string
	groupingVersion            int16
}

func (w *Writer) flush(ctx context.Context, batch []Queued) error {
	if len(batch) == 0 {
		return nil
	}
	secrets := map[int32][]byte{}
	live := make([]Queued, 0, len(batch))
	for _, q := range batch {
		secret, ok := secrets[q.SiteID]
		if !ok {
			var err error
			secret, ok, err = w.secretFor(ctx, q.SiteID, q.Secret)
			if err != nil {
				return err
			}
			if !ok {
				secret = nil // the site is gone; its events are dropped
			}
			secrets[q.SiteID] = secret
		}
		if secret != nil {
			live = append(live, q)
		}
	}
	if len(live) == 0 {
		return nil
	}
	if err := w.ensurePartitions(ctx, live); err != nil {
		return err
	}

	groups := map[[2]string]*issueGroup{}
	var order []*issueGroup
	for _, q := range live {
		e := q.Event
		if e.Kind == "session" || e.Fingerprint == "" {
			continue
		}
		key := [2]string{fmt.Sprint(q.SiteID), e.Fingerprint}
		g := groups[key]
		if g == nil {
			g = &issueGroup{
				siteID: q.SiteID, fingerprint: e.Fingerprint, kind: e.Kind, errType: e.ErrorType,
				title: IssueTitle(e.Kind, e.ErrorType, e.Frames), firstSeen: e.TS, lastSeen: e.TS,
				firstRelease: e.Release, lastRelease: e.Release, releases: map[string]struct{}{},
				level: e.Level, culprit: e.Culprit, groupingVersion: groupingVersion(e),
			}
			groups[key] = g
			order = append(order, g)
		}
		if e.TS.Before(g.firstSeen) {
			g.firstSeen, g.firstRelease = e.TS, e.Release
		}
		if e.TS.After(g.lastSeen) {
			g.lastSeen, g.lastRelease = e.TS, e.Release
			if e.Level != "" {
				g.level = e.Level
			}
			if e.Culprit != "" {
				g.culprit = e.Culprit
			}
		}
		g.count += int64(e.Occurrences)
		g.releases[e.Release] = struct{}{}
	}

	committing := false
	err := w.runTx(ctx, func(tx pgx.Tx) error {
		if len(order) > 0 {
			pgBatch := &pgx.Batch{}
			for _, g := range order {
				pgBatch.Queue(upsertIssueSQL, g.siteID, g.fingerprint, g.kind, g.errType, g.title,
					g.firstSeen, g.lastSeen, g.firstRelease, g.lastRelease, g.count, g.level, g.culprit, g.groupingVersion)
			}
			results := tx.SendBatch(ctx, pgBatch)
			for _, g := range order {
				if err := results.QueryRow().Scan(&g.id, &g.status, &g.resolvedIn, &g.resolvedNext); err != nil {
					_ = results.Close()
					return fmt.Errorf("upsert issue: %w", err)
				}
			}
			if err := results.Close(); err != nil {
				return err
			}
		}
		rows := make([][]any, 0, len(live))
		for _, q := range live {
			e := q.Event
			var issueID any
			if g := groups[[2]string{fmt.Sprint(q.SiteID), e.Fingerprint}]; g != nil && e.Kind != "session" {
				issueID = g.id
			}
			secret := secrets[q.SiteID]
			rows = append(rows, []any{
				q.SiteID, e.TS, q.ReceivedAt, e.ID, issueID, e.Kind, e.Level, e.Handled,
				e.Release, e.Build, e.Environment, e.Platform, e.OSVersion, e.Arch, e.DeviceClass, e.Runtime,
				e.ErrorType, e.ExitReason, e.Signal, e.ExitCode, e.Frames, e.Fingerprint, int32(e.Occurrences),
				mac(secret, e.InstallID), mac(secret, e.SessionID), e.Tags,
				e.MessageHash, e.Culprit, e.Transaction, e.TraceID, e.SpanID, e.UserHash, e.SDKName, e.SDKVersion, e.Dist, e.Symbolication, groupingVersion(e),
			})
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"app_events"}, eventColumns, pgx.CopyFromRows(rows)); err != nil {
			return err
		}
		if err := regress(ctx, tx, order); err != nil {
			return err
		}
		committing = true
		return nil
	})
	if err != nil && committing {
		err = fmt.Errorf("%w: %w", errCommitUnknown, err)
	}
	if err != nil {
		w.mu.Lock()
		clear(w.partitions) // a partition may have been dropped by retention
		w.mu.Unlock()
		return err
	}
	return nil
}

var eventColumns = []string{
	"site_id", "ts", "received_at", "event_id", "issue_id", "kind", "level", "handled",
	"release", "build", "environment", "platform", "os_version", "arch", "device_class", "runtime",
	"error_type", "exit_reason", "signal", "exit_code", "frames", "fingerprint", "occurrences",
	"install_hash", "session_hash", "tags",
	"message_hash", "culprit", "transaction", "trace_id", "span_id", "user_hash", "sdk_name", "sdk_version", "dist", "symbolication", "grouping_version",
}

// groupingVersion is the event's grouping version, defaulting to 1.
func groupingVersion(e Event) int16 {
	if e.GroupingVersion == 0 {
		return 1
	}
	return e.GroupingVersion
}

// upsertIssueSQL creates or updates an issue. Timestamps only widen, so late
// events keep first_seen and last_seen right. It never changes the status:
// regress decides that with the release ordering, which SQL cannot do.
const upsertIssueSQL = `
insert into app_issues (site_id, fingerprint, kind, error_type, title, first_seen, last_seen, first_release, last_release, event_count, level, culprit, grouping_version)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
on conflict (site_id, fingerprint) do update set
  first_seen    = least(app_issues.first_seen, excluded.first_seen),
  last_seen     = greatest(app_issues.last_seen, excluded.last_seen),
  first_release = case when excluded.first_seen < app_issues.first_seen then excluded.first_release else app_issues.first_release end,
  last_release  = case when excluded.last_seen >= app_issues.last_seen then excluded.last_release else app_issues.last_release end,
  level         = case when excluded.last_seen >= app_issues.last_seen and excluded.level <> '' then excluded.level else app_issues.level end,
  culprit       = case when excluded.last_seen >= app_issues.last_seen and excluded.culprit <> '' then excluded.culprit else app_issues.culprit end,
  event_count   = app_issues.event_count + excluded.event_count
returning id, status, resolved_in_release, resolved_next`

// shouldRegress applies the regression rule to the releases of a batch's
// events for a resolved issue. With no recorded release any event regresses.
// A release recorded by "resolve in next release" is the newest release that
// had the bug: events from it or an older build are expected (old builds keep
// running), so only a strictly later release regresses. An explicit fix
// release R regresses on any event from R or a later release.
func shouldRegress(releases []string, resolvedIn *string, next bool, firstSeen map[string]time.Time) bool {
	if resolvedIn == nil {
		return true
	}
	for _, r := range releases {
		c := apprelease.CompareReleases(r, *resolvedIn, firstSeen)
		if (next && c > 0) || (!next && c >= 0) {
			return true
		}
	}
	return false
}

// regress marks resolved issues that received events from a regressing
// release. It runs after the events are copied, so first-seen times include
// the batch.
func regress(ctx context.Context, tx pgx.Tx, groups []*issueGroup) error {
	type site struct{ releases map[string]struct{} }
	bySite := map[int32]*site{}
	var resolved []*issueGroup
	for _, g := range groups {
		if g.status != "resolved" {
			continue
		}
		resolved = append(resolved, g)
		s := bySite[g.siteID]
		if s == nil {
			s = &site{releases: map[string]struct{}{}}
			bySite[g.siteID] = s
		}
		for r := range g.releases {
			s.releases[r] = struct{}{}
		}
		if g.resolvedIn != nil {
			s.releases[*g.resolvedIn] = struct{}{}
		}
	}
	if len(resolved) == 0 {
		return nil
	}
	seen := map[int32]map[string]time.Time{}
	for id, s := range bySite {
		names := make([]string, 0, len(s.releases))
		for r := range s.releases {
			names = append(names, r)
		}
		rows, err := tx.Query(ctx, `select release, min(ts) from app_events where site_id = $1 and release = any($2) group by release`, id, names)
		if err != nil {
			return fmt.Errorf("read release first seen: %w", err)
		}
		m := map[string]time.Time{}
		for rows.Next() {
			var name string
			var ts time.Time
			if err := rows.Scan(&name, &ts); err != nil {
				rows.Close()
				return err
			}
			m[name] = ts
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		seen[id] = m
	}
	for _, g := range resolved {
		releases := make([]string, 0, len(g.releases))
		for r := range g.releases {
			releases = append(releases, r)
		}
		sort.Strings(releases)
		if !shouldRegress(releases, g.resolvedIn, g.resolvedNext, seen[g.siteID]) {
			continue
		}
		if _, err := tx.Exec(ctx, `update app_issues set status = 'regressed', regressed_at = now() where id = $1 and status = 'resolved'`, g.id); err != nil {
			return fmt.Errorf("mark issue regressed: %w", err)
		}
	}
	return nil
}

func mac(secret []byte, value string) []byte {
	if value == "" {
		return nil
	}
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(value))
	return h.Sum(nil)
}

// secretFor returns the app's hash secret, creating it on first use. ok is
// false when the site no longer exists.
func (w *Writer) secretFor(ctx context.Context, siteID int32, hint []byte) ([]byte, bool, error) {
	w.mu.Lock()
	secret, cached := w.secrets[siteID]
	w.mu.Unlock()
	if cached {
		return secret, true, nil
	}
	if len(hint) == 32 {
		w.mu.Lock()
		w.secrets[siteID] = hint
		w.mu.Unlock()
		return hint, true, nil
	}
	fresh := make([]byte, 32)
	if _, err := rand.Read(fresh); err != nil {
		return nil, false, fmt.Errorf("generate hash secret: %w", err)
	}
	if _, err := w.pool.Exec(ctx, `insert into app_hash_secrets(site_id, secret) values ($1, $2) on conflict (site_id) do nothing`, siteID, fresh); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == fkViolation {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("create hash secret: %w", err)
	}
	if err := w.pool.QueryRow(ctx, `select secret from app_hash_secrets where site_id = $1`, siteID).Scan(&secret); err != nil {
		return nil, false, fmt.Errorf("read hash secret: %w", err)
	}
	w.mu.Lock()
	w.secrets[siteID] = secret
	w.mu.Unlock()
	return secret, true, nil
}

// ensurePartitions creates the monthly partitions the batch needs; late
// reports can land in a month the regular lookahead never created.
func (w *Writer) ensurePartitions(ctx context.Context, batch []Queued) error {
	months := map[time.Time]struct{}{}
	for _, q := range batch {
		t := q.Event.TS.UTC()
		months[time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)] = struct{}{}
	}
	for month := range months {
		w.mu.Lock()
		_, done := w.partitions[month]
		w.mu.Unlock()
		if done {
			continue
		}
		if err := db.EnsureAppEventPartitions(ctx, w.pool, month, month.AddDate(0, 1, 0)); err != nil {
			return err
		}
		w.mu.Lock()
		w.partitions[month] = struct{}{}
		w.mu.Unlock()
	}
	return nil
}
