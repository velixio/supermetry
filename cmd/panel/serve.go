package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/api"
	"panel/internal/closer"
	"panel/internal/config"
	"panel/internal/db"
	"panel/internal/dict"
	"panel/internal/enrich"
	"panel/internal/ingest"
	"panel/internal/query"
	"panel/internal/rehydrate"
	"panel/internal/retention"
	"panel/internal/salts"
	"panel/internal/scheduler"
	"panel/internal/session"
	"panel/internal/sites"
	"panel/internal/writer"
)

const (
	httpShutdownTimeout = 10 * time.Second
	partitionLookahead  = 7 * 24 * time.Hour
)

type clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

type serveOptions struct {
	clock clock
}

type serveOption func(*serveOptions)

// withClock replaces the wall clock for same-package integration tests.
// Production callers never pass it.
func withClock(value clock) serveOption {
	return func(options *serveOptions) {
		if value != nil {
			options.clock = value
		}
	}
}

func serve(ctx context.Context, cfg config.Config, options ...serveOption) error {
	settings := serveOptions{clock: wallClock{}}
	for _, option := range options {
		option(&settings)
	}
	if cfg.SitesFile == "" {
		return errors.New("PANEL_SITES_FILE is required")
	}

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	registry, err := newReloadableRegistry(cfg.SitesFile)
	if err != nil {
		return err
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		return fmt.Errorf("load dictionary: %w", err)
	}
	saltStore := salts.New(pool, settings.clock)
	if err := saltStore.Refresh(ctx); err != nil {
		return fmt.Errorf("load salts: %w", err)
	}
	now := settings.clock.Now()
	if err := db.EnsurePartitions(ctx, pool, now.AddDate(0, 0, -1), now.Add(partitionLookahead)); err != nil {
		return fmt.Errorf("ensure startup partitions: %w", err)
	}
	enricher, err := enrich.New(cfg.GeoIPPath)
	if err != nil {
		return fmt.Errorf("load enrichment data: %w", err)
	}
	defer enricher.Close()

	closed, err := loadClosedDays(ctx, pool)
	if err != nil {
		return err
	}
	sessions := session.NewMemory(max(runtime.GOMAXPROCS(0), 16), settings.clock)
	if _, err := rehydrate.Run(ctx, pool, sessions, now); err != nil {
		return fmt.Errorf("rehydrate sessions: %w", err)
	}
	w := writer.New(pool, sessions, cfg.QueueSize, cfg.FlushEvery, cfg.SessionFlush, closed)
	engine := query.NewEngine(pool, dictionary, registry)
	intakeLimiter := ingest.NewTokenBuckets(10_000, 10_000)
	intake := &ingest.Handler{
		Sites: registry, Enricher: enricher, Salts: saltStore, Dict: dictionary,
		Sessions: sessions, Writer: w, Limiter: intakeLimiter,
		TrustedProxies: cfg.TrustedProxies, Clock: settings.clock,
	}

	var ready atomic.Bool
	router := api.NewRouter(api.Deps{
		Config: cfg, Query: engine, Ingest: intake, Sessions: sessions, Sites: registry,
		Now: settings.clock.Now, Ready: ready.Load,
	})
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}
	server := &http.Server{
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       time.Minute,
	}

	writerCtx, cancelWriter := context.WithCancel(context.Background())
	writerDone := make(chan error, 1)
	go func() { writerDone <- w.Run(writerCtx) }()

	schedulerCtx, cancelScheduler := context.WithCancel(context.Background())
	jobs := scheduledJobs(pool, registry, dictionary, saltStore, sessions, closed, settings.clock)
	jobRunner := scheduler.New(pool, nil, jobs...)
	schedulerDone := make(chan error, 1)
	go func() { schedulerDone <- jobRunner.Run(schedulerCtx) }()

	serverDone := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverDone <- err
	}()
	ready.Store(true)

	var cause error
	select {
	case <-ctx.Done():
	case err := <-serverDone:
		cause = err
		serverDone = nil
	case err := <-writerDone:
		cause = err
		writerDone = nil
	case err := <-schedulerDone:
		cause = err
		schedulerDone = nil
	}
	ready.Store(false)

	shutdownCtx, cancelHTTP := context.WithTimeout(context.Background(), httpShutdownTimeout)
	httpErr := server.Shutdown(shutdownCtx)
	cancelHTTP()
	if serverDone != nil {
		cause = errors.Join(cause, <-serverDone)
	}

	cancelWriter()
	if writerDone != nil {
		cause = errors.Join(cause, <-writerDone)
	}
	cancelScheduler()
	if schedulerDone != nil {
		cause = errors.Join(cause, <-schedulerDone)
	}
	return errors.Join(cause, httpErr)
}

func scheduledJobs(
	pool *pgxpool.Pool,
	registry *reloadableRegistry,
	dictionary *dict.Dict,
	saltStore *salts.Store,
	sessions session.Store,
	closed *closedDays,
	clk clock,
) []scheduler.Job {
	return []scheduler.Job{
		{Name: "close-days", Every: time.Minute, Run: func(ctx context.Context) error {
			return closeDueDays(ctx, pool, registry, closed, clk.Now())
		}},
		{Name: "salts", Every: 10 * time.Minute, Run: func(ctx context.Context) error {
			if err := saltStore.Refresh(ctx); err != nil {
				return err
			}
			_, err := saltStore.DeleteExpired(ctx)
			return err
		}},
		{Name: "partitions", Every: time.Hour, Run: func(ctx context.Context) error {
			now := clk.Now()
			return db.EnsurePartitions(ctx, pool, now, now.Add(partitionLookahead))
		}},
		{Name: "retention", Every: time.Hour, Run: func(ctx context.Context) error {
			_, err := retention.Run(ctx, pool, clk.Now())
			return err
		}},
		{Name: "expire-sessions", Every: time.Minute, Run: func(context.Context) error {
			sessions.Expire(clk.Now())
			return nil
		}},
		{Name: "dict-sites-refresh", Every: 30 * time.Second, Run: func(ctx context.Context) error {
			if err := dictionary.Load(ctx); err != nil {
				return err
			}
			return registry.Refresh()
		}},
	}
}

func closeDueDays(ctx context.Context, pool *pgxpool.Pool, registry sites.Registry, closed *closedDays, now time.Time) error {
	due := closer.Due(now, registry, closed.IsClosed)
	rows, err := pool.Query(ctx, `select site_id, day from dirty_day order by day, site_id`)
	if err != nil {
		return fmt.Errorf("load dirty days: %w", err)
	}
	for rows.Next() {
		var siteID int32
		var day time.Time
		if err := rows.Scan(&siteID, &day); err != nil {
			rows.Close()
			return fmt.Errorf("scan dirty day: %w", err)
		}
		due[civilDay(day)] = append(due[civilDay(day)], siteID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate dirty days: %w", err)
	}
	rows.Close()
	for day, siteIDs := range due {
		if _, err := closer.CloseDay(ctx, pool, siteIDs, day); err != nil {
			return err
		}
		closed.Mark(siteIDs, day)
	}
	return nil
}

func closeDays(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("close-days", flag.ContinueOnError)
	siteID := flags.Int("site", 0, "site ID")
	fromValue := flags.String("from", "", "first local day (YYYY-MM-DD)")
	toValue := flags.String("to", "", "last local day (YYYY-MM-DD)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *siteID < 1 || *fromValue == "" || *toValue == "" {
		return errors.New("close-days requires --site, --from and --to")
	}
	from, err := time.Parse(time.DateOnly, *fromValue)
	if err != nil {
		return fmt.Errorf("parse --from: %w", err)
	}
	to, err := time.Parse(time.DateOnly, *toValue)
	if err != nil {
		return fmt.Errorf("parse --to: %w", err)
	}
	if from.After(to) {
		return errors.New("--from must not be after --to")
	}

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	registry, err := sites.LoadFile(cfg.SitesFile)
	if err != nil {
		return err
	}
	if _, ok := registry.Site(int32(*siteID)); !ok {
		return fmt.Errorf("unknown site %d", *siteID)
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		if _, err := closer.CloseDay(ctx, pool, []int32{int32(*siteID)}, day); err != nil {
			return fmt.Errorf("close site %d day %s: %w", *siteID, day.Format(time.DateOnly), err)
		}
	}
	return nil
}

type reloadableRegistry struct {
	path string
	mu   sync.RWMutex
	file *sites.FileRegistry
}

func newReloadableRegistry(path string) (*reloadableRegistry, error) {
	r := &reloadableRegistry{path: path}
	if err := r.Refresh(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *reloadableRegistry) Refresh() error {
	loaded, err := sites.LoadFile(r.path)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.file = loaded
	r.mu.Unlock()
	return nil
}

func (r *reloadableRegistry) ByPublicID(id string) (sites.Site, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.file.ByPublicID(id)
}

func (r *reloadableRegistry) Site(id int32) (sites.Site, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.file.Site(id)
}

func (r *reloadableRegistry) All() []sites.Site {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.file.All()
}

type closedDays struct {
	mu   sync.RWMutex
	days map[string]struct{}
}

func loadClosedDays(ctx context.Context, pool *pgxpool.Pool) (*closedDays, error) {
	index := &closedDays{days: make(map[string]struct{})}
	rows, err := pool.Query(ctx, `select site_id, day from day_close where status = 'done'`)
	if err != nil {
		return nil, fmt.Errorf("load closed days: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var siteID int32
		var day time.Time
		if err := rows.Scan(&siteID, &day); err != nil {
			return nil, fmt.Errorf("scan closed day: %w", err)
		}
		index.days[closedDayKey(siteID, day)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate closed days: %w", err)
	}
	return index, nil
}

func (c *closedDays) IsClosed(siteID int32, day time.Time) bool {
	c.mu.RLock()
	_, ok := c.days[closedDayKey(siteID, day)]
	c.mu.RUnlock()
	return ok
}

func (c *closedDays) Mark(siteIDs []int32, day time.Time) {
	c.mu.Lock()
	for _, siteID := range siteIDs {
		c.days[closedDayKey(siteID, day)] = struct{}{}
	}
	c.mu.Unlock()
}

func closedDayKey(siteID int32, day time.Time) string {
	return strconv.FormatInt(int64(siteID), 10) + ":" + civilDay(day).Format(time.DateOnly)
}

func civilDay(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}
