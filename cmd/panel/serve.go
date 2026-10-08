package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"panel/web"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/alerts"
	"panel/internal/api"
	"panel/internal/closer"
	"panel/internal/config"
	"panel/internal/db"
	"panel/internal/dict"
	"panel/internal/geoupdate"
	"panel/internal/health"
	"panel/internal/heatmap"
	"panel/internal/imports"
	"panel/internal/ingest"
	"panel/internal/minutes"
	platformauth "panel/internal/platform/auth"
	"panel/internal/platform/authn"
	platformcrypto "panel/internal/platform/crypto"
	"panel/internal/platform/goals"
	platformhttp "panel/internal/platform/http"
	"panel/internal/platform/invites"
	"panel/internal/platform/keys"
	"panel/internal/platform/mail"
	"panel/internal/platform/orgs"
	"panel/internal/platform/reports"
	"panel/internal/platform/shares"
	"panel/internal/platform/sitesvc"
	"panel/internal/platform/store"
	"panel/internal/platform/users"
	"panel/internal/profiletombstone"
	"panel/internal/query"
	"panel/internal/rehydrate"
	"panel/internal/replay"
	"panel/internal/retention"
	"panel/internal/salts"
	"panel/internal/scheduler"
	"panel/internal/session"
	"panel/internal/siteexport"
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
	clock     clock
	geoUpdate geoUpdateFunc
	listen    func(network, address string) (net.Listener, error)
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

func withGeoUpdate(value geoUpdateFunc) serveOption {
	return func(options *serveOptions) {
		if value != nil {
			options.geoUpdate = value
		}
	}
}

func withListen(value func(network, address string) (net.Listener, error)) serveOption {
	return func(options *serveOptions) {
		if value != nil {
			options.listen = value
		}
	}
}

func serve(ctx context.Context, cfg config.Config, options ...serveOption) error {
	warnPublicSignup(ctx, slog.Default(), cfg.Signup)
	settings := serveOptions{clock: wallClock{}, geoUpdate: geoupdate.Update, listen: net.Listen}
	for _, option := range options {
		option(&settings)
	}
	geo, err := prepareGeoIP(ctx, cfg, settings.geoUpdate)
	if err != nil {
		return err
	}
	defer geo.Close()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	replayStorage := replay.ResolveStorage(cfg.Replay, cfg.DataDir, replay.StoredStorage{})
	if replayStorage.Backend.V != "disk" {
		return fmt.Errorf("replay storage backend %q is not available", replayStorage.Backend.V)
	}
	replayStore, err := replay.NewDiskStore(replayStorage.Dir.V)
	if err != nil {
		return err
	}

	platformStore := &store.Store{Pool: pool}
	// Replay dashboard reads use the same resolved storage settings as ingest.
	var replayBlobs replay.BlobStore
	replayStorageRaw, err := store.InstanceReplayStorage(ctx, platformStore)
	if err != nil {
		return err
	}
	replayStored, err := replay.OpenStoredStorage(cfg.SecretKey, replayStorageRaw)
	if err != nil {
		return err
	}
	replayStorage := replay.ResolveStorage(cfg.Replay, cfg.DataDir, replayStored)
	if replayStorage.Backend.V == "disk" {
		replayBlobs, err = replay.NewDiskStore(replayStorage.Dir.V)
		if err != nil {
			return err
		}
	}
	databaseRegistry := sitesvc.NewRegistry(platformStore, 30*time.Second)
	defer databaseRegistry.Close()
	var registry sites.Registry = databaseRegistry
	refreshRegistry := databaseRegistry.Invalidate
	var compatibility *compatibilityMode
	var usersExist bool
	if err := pool.QueryRow(ctx, `select exists(select 1 from users)`).Scan(&usersExist); err != nil {
		return fmt.Errorf("check platform users: %w", err)
	}
	if !usersExist && cfg.SitesFile != "" {
		fileRegistry, err := newReloadableRegistry(cfg.SitesFile)
		if err != nil {
			return err
		}
		compatibility = newCompatibilityMode(pool, fileRegistry, databaseRegistry)
		registry = compatibility
		refreshRegistry = compatibility.Refresh
		slog.Warn("using PANEL_SITES_FILE benchmark compatibility because no platform user exists")
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
	enricher := geo.enricher

	closed, err := loadClosedDays(ctx, pool)
	if err != nil {
		return err
	}
	sessions := session.NewMemory(max(runtime.GOMAXPROCS(0), 16), settings.clock)
	if _, err := rehydrate.Run(ctx, pool, sessions, now); err != nil {
		return fmt.Errorf("rehydrate sessions: %w", err)
	}
	w := writer.New(pool, sessions, cfg.QueueSize, cfg.FlushEvery, cfg.SessionFlush, closed)
	profileTombstones := profiletombstone.New(pool, settings.clock)
	if err := profileTombstones.Load(ctx); err != nil {
		return fmt.Errorf("load profile tombstones: %w", err)
	}
	w.SetProfileTombstones(profileTombstones)
	engine := query.NewEngine(pool, dictionary, registry)
	heatmaps := heatmap.New(pool, registry)
	sender := mail.NewSMTP(cfg.SMTP)
	secretBox, err := platformcrypto.NewSecretBox(cfg.SecretKey, "panel/totp/v1")
	if err != nil {
		return fmt.Errorf("initialize TOTP encryption: %w", err)
	}
	accounts := users.New(platformStore)
	authService := platformauth.New(platformStore, sender, secretBox, cfg.BaseURL)
	orgService := orgs.New(platformStore)
	keyService := keys.New(platformStore)
	defer keyService.Close()
	shareService := shares.New(platformStore)
	inviteService := invites.New(platformStore, accounts, sender, cfg.BaseURL)
	definitionService := goals.New(platformStore, 30*time.Second)
	defer definitionService.Close()
	// goal_id filters resolve goal definitions from this in-memory snapshot.
	engine.SetGoalLookup(definitionService)
	reportService := reports.New(platformStore, engine, registry, sender)
	alertBox, err := platformcrypto.NewSecretBox(cfg.SecretKey, "panel/alerts/v1")
	if err != nil {
		return fmt.Errorf("initialize alert encryption: %w", err)
	}
	webhookClient := alerts.NewWebhookClient(alerts.WebhookOptions{AllowPrivate: cfg.AllowPrivateWebhooks})
	alertDispatcher := alerts.NewDispatcher(sender, webhookClient)
	alertService := alerts.New(platformStore, alertBox, alertDispatcher)
	alertMetrics := alerts.NewEngineSource(engine, definitionService)
	alertEvaluator := alerts.NewEvaluator(alertService, alertMetrics, registry, sessions, alertDispatcher, settings.clock.Now,
		func(alertID, channelID int64, kind alerts.ChannelKind, deliveryErr error) {
			health.AlertDeliveryFailures.Add(1)
			slog.Warn("alert delivery failed", "alert_id", alertID, "channel_id", channelID, "channel_kind", kind, "error", deliveryErr)
		})
	importStore := imports.NewStore(pool)
	if err := importStore.FailRunningAtStartup(ctx); err != nil {
		return fmt.Errorf("recover interrupted imports: %w", err)
	}
	importManager := imports.NewManager(importStore, 1)
	exporter := siteexport.New(pool)
	apiAuthenticator := authn.New(authService, keyService, shareService, orgService)
	serviceAuthenticator := platformhttp.NewAuthenticator(authService, keyService, shareService, orgService)
	siteService := sitesvc.New(platformStore, databaseRegistry)
	platformHandler := platformhttp.New(platformhttp.Deps{
		BaseURL: cfg.BaseURL, TrackerPath: cfg.TrackerPath,
		Snippet: func(publicID string, full bool) string {
			return sitesvc.Snippet(cfg.BaseURL, cfg.TrackerPath, cfg.EventPath, publicID, full)
		},
		SecureCookies:  strings.HasPrefix(strings.ToLower(cfg.BaseURL), "https://"),
		Signup:         cfg.Signup,
		TrustedProxies: cfg.TrustedProxies,
		Authenticator:  serviceAuthenticator,
		Sites:          registry,
		Mutations: platformhttp.Services{
			Sites: siteService,
			Keys:  keyService,
			Store: platformStore,
		},
		Store:             platformStore,
		Definitions:       definitionService,
		Auth:              authService,
		Orgs:              orgService,
		Keys:              keyService,
		Invites:           inviteService,
		Shares:            shareService,
		Reports:           reportService,
		Alerts:            alertService,
		Query:             engine,
		Imports:           importStore,
		ImportQueue:       importManager,
		ProfileTombstones: profileTombstones,
		ReplayStore:       replayStore,
		Exporter:          exporter,
		ReplayStore:       replayBlobs,
		ReplayEnv:         cfg.Replay,
		DataDir:           cfg.DataDir,
		SecretKey:         cfg.SecretKey,
		AdminAPIKey:       cfg.AdminAPIKey,
		ReplayRefresh:     refreshRegistry,
	})
	var publicAuthenticator api.Authenticator = apiAuthenticator
	if compatibility != nil {
		publicAuthenticator = &compatibilityAuthenticator{
			mode: compatibility, platform: apiAuthenticator, fallback: api.StaticKey(cfg.AdminAPIKey),
		}
		platformHandler = transitionFromCompatibility(platformHandler, compatibility)
	}
	intakeLimiter := ingest.NewTokenBuckets(10_000, 10_000)
	intake := &ingest.Handler{
		Sites: registry, Enricher: enricher, Salts: saltStore, Dict: dictionary,
		Sessions: sessions, Writer: w, Limiter: intakeLimiter,
		TrustedProxies: cfg.TrustedProxies, Clock: settings.clock,
		ProfileTombstones: profileTombstones,
		Diag:              os.Getenv("PANEL_INGEST_DIAG") == "1",
	}

	// Replay owns a separate limiter, upload semaphore, blob store and index
	// queue so replay pressure cannot consume the analytics ingest queue.
	var replayHandler http.Handler
	var replayIndex *replay.IndexWriter
	if cfg.Replay.Enabled {
		var rawStorage []byte
		if err := pool.QueryRow(ctx, `select coalesce(settings->'replay_storage', '{}'::jsonb) from instance_settings where id`).Scan(&rawStorage); err != nil {
			return fmt.Errorf("load replay storage settings: %w", err)
		}
		storedStorage, err := replay.OpenStoredStorage(cfg.SecretKey, rawStorage)
		if err != nil {
			return err
		}
		storage := replay.ResolveStorage(cfg.Replay, cfg.DataDir, storedStorage)
		if storage.Backend.V != "disk" {
			return fmt.Errorf("replay storage backend %q is not available in this build", storage.Backend.V)
		}
		blobStore, err := replay.NewDiskStore(storage.Dir.V)
		if err != nil {
			return err
		}
		replayIndex = replay.NewIndexWriter(pool, 5*time.Second, func() { health.ReplayDropped.Add(1) })
		replayHandler = replay.NewHandler(replay.HandlerDeps{
			Sites: registry, Salts: saltStore, Sessions: sessions, Blobs: blobStore, Index: replayIndex,
			Replay: cfg.Replay, Limiter: ingest.NewTokenBuckets(250, 500), TrustedProxies: cfg.TrustedProxies,
		}, replay.WithClock(settings.clock), replay.WithDiskGuard(func() bool { return false }))
	}

	var ready atomic.Bool
	router := api.NewRouter(api.Deps{
		UI:     web.Handler(),
		Config: cfg, Query: engine, SessionExplore: engine, Heatmaps: heatmaps, Ingest: intake, Replay: replayHandler, Sessions: sessions, MinuteSource: minutes.NewStore(pool), Sites: registry, Dictionary: dictionary,
		PublicAuth: publicAuthenticator, PrivateAuth: apiAuthenticator, Platform: platformHandler,
		Now: settings.clock.Now, Ready: ready.Load,
	})
	listener, err := settings.listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}
	if setupURL, ok := authService.SetupURL(ctx); ok {
		fmt.Fprintln(os.Stdout, setupURL)
	}
	server := &http.Server{
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       time.Minute,
	}

	writerCtx, cancelWriter := context.WithCancel(context.Background())
	writerDone := make(chan error, 1)
	go func() { writerDone <- w.Run(writerCtx) }()
	var cancelReplayIndex context.CancelFunc
	var replayIndexDone chan error
	if replayIndex != nil {
		var replayIndexCtx context.Context
		replayIndexCtx, cancelReplayIndex = context.WithCancel(context.Background())
		replayIndexDone = make(chan error, 1)
		go func() { replayIndexDone <- replayIndex.Run(replayIndexCtx) }()
	}

	schedulerCtx, cancelScheduler := context.WithCancel(context.Background())
	jobs := scheduledJobs(pool, registry, refreshRegistry, dictionary, saltStore, sessions, closed, reportService, alertEvaluator, geo, replayStore, cfg.Replay, settings.clock)
	jobs = append(jobs, profileTombstones.Job())
	jobRunner := scheduler.New(pool, nil, jobs...)
	schedulerDone := make(chan error, 1)
	go func() { schedulerDone <- jobRunner.Run(schedulerCtx) }()
	geoRefreshDone := startGeoIPRefresh(schedulerCtx, geo, func(err error) {
		slog.Warn("GeoIP background refresh failed", "error", err)
	})
	importCtx, cancelImports := context.WithCancel(context.Background())
	importDone := make(chan error, 1)
	go func() {
		err := importManager.Run(importCtx)
		if errors.Is(err, context.Canceled) {
			err = nil
		}
		importDone <- err
	}()

	serverDone := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverDone <- err
	}()
	ready.Store(true)
	slog.Info("server started",
		"listen", listener.Addr().String(),
		"base_url", cfg.BaseURL,
		"geoip", enricher.GeoIPSource(),
		"trusted_proxies", len(cfg.TrustedProxies),
		"ingest_diag", intake.Diag,
	)

	var cause error
	select {
	case <-ctx.Done():
	case err := <-serverDone:
		cause = err
		serverDone = nil
	case err := <-writerDone:
		cause = err
		writerDone = nil
	case err := <-replayIndexDone:
		cause = err
		replayIndexDone = nil
	case err := <-schedulerDone:
		cause = err
		schedulerDone = nil
	case err := <-importDone:
		cause = err
		importDone = nil
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
	if cancelReplayIndex != nil {
		cancelReplayIndex()
	}
	if replayIndexDone != nil {
		cause = errors.Join(cause, <-replayIndexDone)
	}
	cancelScheduler()
	if schedulerDone != nil {
		cause = errors.Join(cause, <-schedulerDone)
	}
	if geoRefreshDone != nil {
		<-geoRefreshDone
	}
	cancelImports()
	if importDone != nil {
		cause = errors.Join(cause, <-importDone)
	}
	cause = errors.Join(cause, eraseDeletedSites(context.Background(), pool, replayStore, settings.clock.Now()))
	return errors.Join(cause, httpErr)
}

type compatibilityMode struct {
	active   atomic.Bool
	pool     *pgxpool.Pool
	file     *reloadableRegistry
	database *sitesvc.Registry
}

func newCompatibilityMode(pool *pgxpool.Pool, file *reloadableRegistry, database *sitesvc.Registry) *compatibilityMode {
	mode := &compatibilityMode{pool: pool, file: file, database: database}
	mode.active.Store(true)
	return mode
}

func (m *compatibilityMode) ByPublicID(id string) (sites.Site, bool) {
	if m.active.Load() {
		return m.file.ByPublicID(id)
	}
	return m.database.ByPublicID(id)
}

func (m *compatibilityMode) Site(id int32) (sites.Site, bool) {
	if m.active.Load() {
		return m.file.Site(id)
	}
	return m.database.Site(id)
}

func (m *compatibilityMode) All() []sites.Site {
	if m.active.Load() {
		return m.file.All()
	}
	return m.database.All()
}

func (m *compatibilityMode) Refresh() error {
	if err := m.disableWhenUsersExist(context.Background()); err != nil {
		return err
	}
	if m.active.Load() {
		return m.file.Refresh()
	}
	return m.database.Invalidate()
}

func (m *compatibilityMode) disableWhenUsersExist(ctx context.Context) error {
	if !m.active.Load() {
		return nil
	}
	var exists bool
	if err := m.pool.QueryRow(ctx, `select exists(select 1 from users)`).Scan(&exists); err != nil {
		return fmt.Errorf("check platform users: %w", err)
	}
	if exists {
		if err := m.database.Invalidate(); err != nil {
			return err
		}
		m.active.Store(false)
	}
	return nil
}

type compatibilityAuthenticator struct {
	mode     *compatibilityMode
	platform api.Authenticator
	fallback api.Authenticator
}

func (a *compatibilityAuthenticator) Authenticate(request *http.Request) (api.Principal, bool) {
	if err := a.mode.disableWhenUsersExist(request.Context()); err != nil {
		return api.Principal{}, false
	}
	if a.mode.active.Load() {
		return a.fallback.Authenticate(request)
	}
	return a.platform.Authenticate(request)
}

func transitionFromCompatibility(next http.Handler, mode *compatibilityMode) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		next.ServeHTTP(w, request)
		if request.Method == http.MethodPost && request.URL.Path == "/api/private/setup" {
			_ = mode.disableWhenUsersExist(context.WithoutCancel(request.Context()))
		}
	})
}

func scheduledJobs(
	pool *pgxpool.Pool,
	registry sites.Registry,
	refreshRegistry func() error,
	dictionary *dict.Dict,
	saltStore *salts.Store,
	sessions session.Store,
	closed *closedDays,
	reportService *reports.Reports,
	alertJobs interface{ Job() scheduler.Job },
	geo *geoService,
	replayStore replay.BlobStore,
	replayEnvironment config.ReplayEnv,
	clk clock,
) []scheduler.Job {
	jobs := []scheduler.Job{
		minutes.NewStore(pool).Job(clk.Now),
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
			return refreshRegistry()
		}},
		{Name: "site-erasure", Every: time.Minute, Run: func(ctx context.Context) error {
			return eraseDeletedSites(ctx, pool, replayStore, clk.Now())
		}},
		{Name: "user-session-cleanup", Every: time.Hour, Run: func(ctx context.Context) error {
			now := clk.Now()
			_, err := pool.Exec(ctx, `delete from user_sessions where expires_at <= $1 or last_seen_at <= $2`, now, now.Add(-14*24*time.Hour))
			return err
		}},
		{Name: "invitation-cleanup", Every: time.Hour, Run: func(ctx context.Context) error {
			_, err := pool.Exec(ctx, `delete from invitations where expires_at <= $1 or accepted_at is not null or revoked_at is not null`, clk.Now())
			return err
		}},
	}
	if replayStore != nil {
		jobs = append(jobs, scheduler.Job{Name: "replay-retention", Every: time.Hour, Run: func(ctx context.Context) error {
			return replay.RunRetentionConfigured(ctx, pool, replayStore, clk.Now(), replayEnvironment)
		}})
	}
	if geo != nil && geo.maxMind {
		jobs = append(jobs, scheduler.Job{Name: "geoip-update", Every: geoIPUpdateInterval, Run: func(ctx context.Context) error {
			_, err := geo.Refresh(ctx, false)
			return err
		}})
	}
	if reportService != nil {
		jobs = append(jobs, reportService.Job())
	}
	if alertJobs != nil {
		jobs = append(jobs, alertJobs.Job())
	}
	return jobs
}

func eraseDeletedSites(ctx context.Context, pool *pgxpool.Pool, replayStore replay.BlobStore, now time.Time) error {
	rows, err := pool.Query(ctx, `select id from sites where deleted_at is not null order by id`)
	if err != nil {
		return fmt.Errorf("load sites pending erasure: %w", err)
	}
	var siteIDs []int32
	for rows.Next() {
		var siteID int32
		if err := rows.Scan(&siteID); err != nil {
			rows.Close()
			return fmt.Errorf("scan site pending erasure: %w", err)
		}
		siteIDs = append(siteIDs, siteID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate sites pending erasure: %w", err)
	}
	rows.Close()
	for _, siteID := range siteIDs {
		if replayStore != nil {
			if err := replay.EraseSite(ctx, pool, replayStore, siteID); err != nil {
				return err
			}
		}
		if err := retention.Erase(ctx, pool, siteID, 10_000, 10*time.Millisecond); err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, `update sites set settings=jsonb_set(settings,'{erased_at}',to_jsonb($2::text)) where id=$1`, siteID, now.UTC().Format(time.RFC3339)); err != nil {
			return fmt.Errorf("mark site %d erased: %w", siteID, err)
		}
	}
	return nil
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
