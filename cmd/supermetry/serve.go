package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"supermetry/web"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/alerts"
	"supermetry/internal/api"
	"supermetry/internal/closer"
	"supermetry/internal/config"
	"supermetry/internal/db"
	"supermetry/internal/demo"
	"supermetry/internal/dict"
	"supermetry/internal/dropstats"
	"supermetry/internal/geoupdate"
	"supermetry/internal/health"
	"supermetry/internal/heatmap"
	"supermetry/internal/imports"
	"supermetry/internal/ingest"
	"supermetry/internal/minutes"
	"supermetry/internal/platform/audit"
	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/authn"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/goals"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/invites"
	"supermetry/internal/platform/keys"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/orgs"
	"supermetry/internal/platform/reports"
	"supermetry/internal/platform/shares"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/platform/users"
	"supermetry/internal/profileerasure"
	"supermetry/internal/profiletombstone"
	"supermetry/internal/query"
	"supermetry/internal/rehydrate"
	"supermetry/internal/replay"
	"supermetry/internal/retention"
	"supermetry/internal/salts"
	"supermetry/internal/scheduler"
	"supermetry/internal/session"
	"supermetry/internal/siteexport"
	"supermetry/internal/sites"
	"supermetry/internal/writer"
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
	if err := cfg.Tracking.ValidateUpgrade(false); err != nil {
		return err
	}
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
	platformStore := &store.Store{Pool: pool}
	// One replay blob store serves ingest, dashboard reads, retention and erasure.
	var replayStore replay.BlobStore
	replayStorageRaw, err := store.InstanceReplayStorage(ctx, platformStore)
	if err != nil {
		return err
	}
	replayStored, err := replay.OpenStoredStorage(cfg.SecretKey, replayStorageRaw)
	if err != nil {
		return err
	}
	replayStorage := replay.ResolveStorage(cfg.Replay, cfg.DataDir, replayStored)
	replayStore, err = openReplayStore(replayStorage, filepath.Join(cfg.DataDir, "replay-spill"))
	if err != nil {
		return fmt.Errorf("initialize replay %s storage: %w", replayStorage.Backend.V, err)
	}
	replayIndex := replay.NewIndexWriter(pool, 5*time.Second, func() { health.ReplayDropped.Add(1) })
	var replayDiskLow atomic.Bool
	var refreshReplayDiskGuard func()
	if replayStorage.Backend.V == "disk" {
		refreshReplayDiskGuard = func() {
			low := replay.DiskLow(replayStorage.Dir.V)
			replayDiskLow.Store(low)
			if low {
				health.ReplayDiskLow.Store(1)
			} else {
				health.ReplayDiskLow.Store(0)
			}
		}
		refreshReplayDiskGuard()
	} else {
		health.ReplayDiskLow.Store(0)
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
		slog.Warn("using SUPERMETRY_SITES_FILE benchmark compatibility because no platform user exists")
	}
	if err := validateTrackingStartup(ctx, pool, cfg.Tracking, registry.All()); err != nil {
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
	if err := db.EnsurePartitions(ctx, pool, now.AddDate(0, 0, -2), now.Add(partitionLookahead)); err != nil {
		return fmt.Errorf("ensure startup partitions: %w", err)
	}
	enricher := geo.enricher
	if spamFile := strings.TrimSpace(os.Getenv("SUPERMETRY_REFERRER_SPAM_FILE")); spamFile != "" {
		if err := enricher.MergeReferrerSpamFile(spamFile); err != nil {
			return fmt.Errorf("load operator referrer spam file: %w", err)
		}
	}

	closed, err := loadClosedDays(ctx, pool)
	if err != nil {
		return err
	}
	sessions := session.NewMemory(max(runtime.GOMAXPROCS(0), 16), settings.clock)
	if _, err := rehydrate.Run(ctx, pool, sessions, now); err != nil {
		return fmt.Errorf("rehydrate sessions: %w", err)
	}
	w := writer.New(pool, sessions, cfg.QueueSize, cfg.FlushEvery, cfg.SessionFlush)
	w.SetClock(settings.clock)
	profileTombstones := profiletombstone.New(pool, settings.clock)
	profileTombstones.SetSessions(sessions)
	if err := profileTombstones.Load(ctx); err != nil {
		return fmt.Errorf("load profile tombstones: %w", err)
	}
	w.SetProfileTombstones(profileTombstones)
	w.SetTrackingPolicy(cfg.Tracking.Mode, registry)
	w.SetPathPolicy(cfg.TrackingPaths, dictionary)
	engine := query.NewEngine(pool, dictionary, registry)
	engine.SetQueryTimeout(cfg.QueryTimeout)
	engine.SetCache(query.NewCache(0))
	engine.SetTrackingMode(cfg.Tracking.Mode)
	engine.SetPathPolicy(cfg.TrackingPaths)
	profileErasures := profileerasure.New(pool)
	profileErasureWorker := profileerasure.NewWorker(pool, profileErasures, replayStore, settings.clock, engine.InvalidateSite)
	rollupBackfill := closer.NewRollupBackfill(pool, query.CurrentRollupLayout(), pauseRollupBackfill, engine.InvalidateSite)
	heatmaps := heatmap.New(pool, registry)
	sender := mail.NewSMTP(cfg.SMTP)
	secretBox, err := platformcrypto.NewSecretBox(cfg.SecretKey, "supermetry/totp/v1")
	if err != nil {
		return fmt.Errorf("initialize TOTP encryption: %w", err)
	}
	accounts := users.New(platformStore)
	authService := platformauth.New(platformStore, sender, secretBox, cfg.BaseURL, platformauth.WithAuditSecret(cfg.SecretKey))
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
	reportService.SetTrackingMode(cfg.Tracking.Mode)
	alertBox, err := platformcrypto.NewSecretBox(cfg.SecretKey, "supermetry/alerts/v1")
	if err != nil {
		return fmt.Errorf("initialize alert encryption: %w", err)
	}
	webhookClient := alerts.NewWebhookClient(alerts.WebhookOptions{AllowPrivate: cfg.AllowPrivateWebhooks})
	telegramClient := alerts.NewTelegramClient(alerts.TelegramOptions{})
	alertDispatcher := alerts.NewDispatcher(sender, webhookClient, telegramClient)
	alertService := alerts.New(platformStore, alertBox, alertDispatcher)
	alertMetrics := alerts.NewEngineSource(engine, definitionService)
	alertEvaluator := alerts.NewEvaluator(alertService, alertMetrics, registry, sessions, alertDispatcher, settings.clock.Now,
		func(alertID, channelID int64, kind alerts.ChannelKind, deliveryErr error) {
			health.AlertDeliveryFailures.Add(1)
			slog.Warn("alert delivery failed", "alert_id", alertID, "channel_id", channelID, "channel_kind", kind, "error", deliveryErr)
		})
	demoService := demo.New(demo.Deps{
		Pool: pool, Registry: databaseRegistry, Dict: dictionary,
		Authz: func(_ context.Context, principal authz.Principal, orgID int64) error {
			if principal.OrgID != orgID || !authz.Can(principal, authz.ActionSiteCreate, 0) {
				return sitesvc.ErrForbidden
			}
			return nil
		},
		Audit: func(ctx context.Context, tx pgx.Tx, principal authz.Principal, _ int64, siteID int32) error {
			return audit.Write(ctx, tx, principal, "site.created", "site", int64(siteID), map[string]any{"demo": true})
		},
		Blobs: replayStore, Index: replayIndex, Alerts: alertService, Definitions: definitionService,
		Now: settings.clock.Now, Sessions: sessions, Activity: sessions, Writer: w,
	})
	importStore := imports.NewStore(pool, engine.InvalidateSite)
	if err := importStore.FailRunningAtStartup(ctx); err != nil {
		return fmt.Errorf("recover interrupted imports: %w", err)
	}
	importManager := imports.NewManager(importStore, 1)
	exporter := siteexport.New(pool)
	exporter.SetTrackingMode(cfg.Tracking.Mode)
	apiAuthenticator := authn.New(authService, keyService, shareService, orgService)
	serviceAuthenticator := platformhttp.NewAuthenticator(authService, keyService, shareService, orgService)
	siteService := sitesvc.New(platformStore, databaseRegistry, cfg.Tracking.Mode)
	siteService.SetQueryInvalidator(engine.InvalidateSite)
	platformHandler := platformhttp.New(platformhttp.Deps{
		TrackingMode: cfg.Tracking.Mode,
		BaseURL:      cfg.BaseURL, HomepageURL: cfg.HomepageURL, TrackerPath: cfg.TrackerPath,
		Snippet: func(publicID string, full bool) string {
			return sitesvc.Snippet(cfg.BaseURL, cfg.TrackerPath, cfg.EventPath, publicID, full)
		},
		SecureCookies:  strings.HasPrefix(strings.ToLower(cfg.BaseURL), "https://"),
		Signup:         cfg.Signup,
		TrustedProxies: cfg.TrustedProxies,
		SessionCountry: enricher.Country,
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
		ProfileErasures:   profileErasures,
		ReplayStore:       replayStore,
		Exporter:          exporter,
		ReplayEnv:         cfg.Replay,
		DataDir:           cfg.DataDir,
		SecretKey:         cfg.SecretKey,
		AdminAPIKey:       cfg.AdminAPIKey,
		ReplayRefresh:     refreshRegistry,
		Demo:              demoService,
	})
	var publicAuthenticator api.Authenticator = apiAuthenticator
	if compatibility != nil {
		publicAuthenticator = &compatibilityAuthenticator{
			mode: compatibility, platform: apiAuthenticator, fallback: api.StaticKey(cfg.AdminAPIKey),
		}
		platformHandler = transitionFromCompatibility(platformHandler, compatibility)
	}
	ipRate, siteRate, globalRate := clampIngestRates(slog.Default(), cfg.IngestRateIP, cfg.IngestRateSite, cfg.IngestRateGlobal)
	intakeLimiter := ingest.NewIngestRateLimiter(ipRate, siteRate, globalRate)
	dropRecorder := dropstats.New(pool, dropstats.Options{})
	intake := &ingest.Handler{
		TrackingMode: cfg.Tracking.Mode,
		PathPolicy:   cfg.TrackingPaths,
		Sites:        registry, Enricher: enricher, Salts: saltStore, Dict: dictionary,
		Sessions: sessions, Writer: w, Limiter: intakeLimiter,
		TrustedProxies: cfg.TrustedProxies, Clock: settings.clock,
		Dedup:             ingest.NewDeduplicator(cfg.DedupEntries),
		DropStats:         dropRecorder,
		ProfileTombstones: profileTombstones,
		Diag:              os.Getenv("SUPERMETRY_INGEST_DIAG") == "1",
	}

	// Replay owns a separate limiter, upload semaphore, blob store and index
	// queue so replay pressure cannot consume the analytics ingest queue.
	var replayHandler http.Handler
	var replayCSSHandler http.Handler
	if cfg.Replay.Enabled && replayStore != nil {
		replayLimiter := ingest.NewTokenBuckets(250, 500)
		replayHandler = replay.NewHandler(replay.HandlerDeps{
			Sites: registry, Salts: saltStore, Sessions: sessions, Blobs: replayStore, Index: replayIndex,
			Replay: cfg.Replay, Limiter: replayLimiter, TrustedProxies: cfg.TrustedProxies,
		}, replay.WithClock(settings.clock), replay.WithDiskGuard(replayDiskLow.Load),
			replay.WithSiteByteBudget(replay.DefaultSiteByteBudget, replay.DefaultSiteByteWindow))
		replayCSSHandler = replay.NewCSSHandler(replayStore, func(publicID string) (int32, bool) {
			site, ok := registry.ByPublicID(publicID)
			return site.ID, ok
		}, make(chan struct{}, 16), func() { health.ReplayDropped.Add(1) },
			replay.WithCSSLimiter(replayLimiter, cfg.TrustedProxies), replay.WithCSSDiskGuard(replayDiskLow.Load))
		replayCSSHandler = replay.CollectionGuard(registry, cfg.Replay, replayCSSHandler)
	}

	var ready atomic.Bool
	router := api.NewRouter(api.Deps{
		UI:     web.Handler(),
		Config: cfg, Query: engine, SessionExplore: engine, Heatmaps: heatmaps, Ingest: intake, Replay: replayHandler, ReplayCSS: replayCSSHandler, Sessions: sessions, MinuteSource: minutes.NewStore(pool), Sites: registry, Dictionary: dictionary,
		PublicAuth: publicAuthenticator, PrivateAuth: apiAuthenticator, Platform: platformHandler,
		Now: settings.clock.Now, Ready: func() bool { return ready.Load() && postgresReady(pool) },
		RollupBackfillProgress: func(ctx context.Context) (bool, int, int, error) {
			progress, err := rollupBackfill.Progress(ctx)
			return progress.Running, progress.DaysDone, progress.TotalDays, err
		},
		FailedProfileErasures: func(ctx context.Context) (int64, error) {
			var count int64
			err := pool.QueryRow(ctx, `select count(*) from profile_erasure_requests where status='failed'`).Scan(&count)
			return count, err
		},
		DropStats: dropRecorder,
	})
	listener, err := settings.listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}
	warnLoopbackProxyRisk(slog.Default(), listener.Addr(), cfg.TrustedProxies)
	if setupURL, ok := authService.SetupURL(ctx); ok {
		fmt.Fprintln(os.Stdout, setupURL)
	}
	server := newHTTPServer(router, cfg)

	writerCtx, cancelWriter := context.WithCancel(context.Background())
	writerDone := make(chan error, 1)
	go func() { writerDone <- w.Run(writerCtx) }()
	dropStatsCtx, cancelDropStats := context.WithCancel(context.Background())
	dropStatsDone := make(chan error, 1)
	go func() { dropStatsDone <- dropRecorder.Run(dropStatsCtx) }()
	replayIndexCtx, cancelReplayIndex := context.WithCancel(context.Background())
	replayIndexDone := make(chan error, 1)
	go func() { replayIndexDone <- replayIndex.Run(replayIndexCtx) }()
	demoResumeCtx, cancelDemoResume := context.WithCancel(context.Background())
	demoResumeDone := make(chan struct{})
	go func() {
		defer close(demoResumeDone)
		if err := demoService.ResumeUnseeded(demoResumeCtx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("resume demo seeding failed", "error", err)
		}
	}()

	schedulerCtx, cancelScheduler := context.WithCancel(context.Background())
	jobs := scheduledJobs(pool, registry, refreshRegistry, dictionary, saltStore, sessions, closed, reportService, alertEvaluator, geo, replayStore, cfg.Replay, cfg.DataDir, cfg.SecretKey, refreshReplayDiskGuard, settings.clock, engine.InvalidateSite)
	jobs = append(jobs, profileErasureWorker.Job(), rollupBackfill.Job(), demoService.Job(), demoService.ExpiryJob())
	jobRunner := scheduler.New(pool, nil, jobs...)
	schedulerDone := make(chan error, 1)
	go func() { schedulerDone <- jobRunner.Run(schedulerCtx) }()
	tombstoneDone := make(chan error, 1)
	go func() { tombstoneDone <- profileTombstones.Run(schedulerCtx) }()
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
	case err := <-dropStatsDone:
		cause = err
		dropStatsDone = nil
	case err := <-replayIndexDone:
		cause = err
		replayIndexDone = nil
	case err := <-schedulerDone:
		cause = err
		schedulerDone = nil
	case err := <-tombstoneDone:
		cause = err
		tombstoneDone = nil
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
	authShutdownCtx, cancelAuth := context.WithTimeout(context.Background(), httpShutdownTimeout)
	cause = errors.Join(cause, authService.Shutdown(authShutdownCtx))
	cancelAuth()

	cancelScheduler()
	if schedulerDone != nil {
		cause = errors.Join(cause, <-schedulerDone)
	}
	if tombstoneDone != nil {
		cause = errors.Join(cause, <-tombstoneDone)
	}
	if geoRefreshDone != nil {
		<-geoRefreshDone
	}
	cancelDemoResume()
	demoService.Close()
	<-demoResumeDone
	cancelWriter()
	if writerDone != nil {
		cause = errors.Join(cause, <-writerDone)
	}
	cancelDropStats()
	if dropStatsDone != nil {
		cause = errors.Join(cause, <-dropStatsDone)
	}
	cancelReplayIndex()
	if replayIndexDone != nil {
		cause = errors.Join(cause, <-replayIndexDone)
	}
	cancelImports()
	if importDone != nil {
		cause = errors.Join(cause, <-importDone)
	}
	cause = errors.Join(cause, eraseDeletedSites(context.Background(), pool, replayStore, settings.clock.Now()))
	if closer, ok := replayStore.(interface{ Close(context.Context) error }); ok {
		closeCtx, cancelReplayStore := context.WithTimeout(context.Background(), httpShutdownTimeout)
		cause = errors.Join(cause, closer.Close(closeCtx))
		cancelReplayStore()
	}
	return errors.Join(cause, httpErr)
}

func postgresReady(pool *pgxpool.Pool) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var one int
	return pool.QueryRow(ctx, `select 1`).Scan(&one) == nil && one == 1
}

func clampIngestRates(logger *slog.Logger, ipRate, siteRate, globalRate int) (int, int, int) {
	configuredIP, configuredSite := ipRate, siteRate
	siteRate = min(siteRate, globalRate)
	ipRate = min(ipRate, siteRate)
	if ipRate != configuredIP || siteRate != configuredSite {
		logger.Warn("ingest rate limits were clamped to preserve ip <= site <= global",
			"configured_ip", configuredIP, "configured_site", configuredSite, "configured_global", globalRate,
			"effective_ip", ipRate, "effective_site", siteRate, "effective_global", globalRate)
	}
	return ipRate, siteRate, globalRate
}

func warnLoopbackProxyRisk(logger *slog.Logger, addr net.Addr, trustedProxies []netip.Prefix) {
	if len(trustedProxies) != 0 {
		return
	}
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok || !tcpAddr.IP.IsLoopback() {
		return
	}
	logger.Warn("server listens on loopback without SUPERMETRY_TRUSTED_PROXIES; visitors behind a same-host proxy may all share one IP; configure SUPERMETRY_TRUSTED_PROXIES with the proxy address")
}

func newHTTPServer(handler http.Handler, cfg config.Config) *http.Server {
	return &http.Server{
		Handler:           appSecurityHeaders(cfg.BaseURL, handler),
		ReadTimeout:       cfg.HTTPReadTimeout,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       time.Minute,
	}
}

func appSecurityHeaders(baseURL string, next http.Handler) http.Handler {
	// The script hash permits only the pre-paint theme initializer in web/index.html.
	const policy = "default-src 'self'; script-src 'self' 'sha256-SuOncodWHu+gLg1OlZCyogivDxh1qcH4Utmkganh+Pg='; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; font-src 'self'; connect-src 'self'; frame-src 'self' blob: https: http:; worker-src 'self' blob:; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"
	hsts := strings.HasPrefix(strings.ToLower(baseURL), "https://")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", policy)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if hsts {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func pauseRollupBackfill(ctx context.Context) error {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func openReplayStore(settings replay.StorageSettings, spillDir string) (replay.BlobStore, error) {
	switch settings.Backend.V {
	case "disk":
		return replay.NewDiskStore(settings.Dir.V)
	case "s3":
		return replay.NewS3Store(settings.S3.V, spillDir)
	default:
		return nil, fmt.Errorf("unsupported backend %q", settings.Backend.V)
	}
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
	dataDir string,
	secretKey []byte,
	refreshReplayDiskGuard func(),
	clk clock,
	closeInvalidators ...func(int32),
) []scheduler.Job {
	jobs := []scheduler.Job{
		minutes.NewStore(pool).Job(clk.Now),
		{Name: "close-days", Every: time.Minute, Run: func(ctx context.Context) error {
			return closeDueDays(ctx, pool, registry, closed, clk.Now(), closeInvalidators...)
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
			return db.EnsurePartitions(ctx, pool, now.AddDate(0, 0, -2), now.Add(partitionLookahead))
		}},
		{Name: "retention", Every: time.Hour, Run: func(ctx context.Context) error {
			_, err := retention.Run(ctx, pool, clk.Now())
			return err
		}},
		{Name: "dict-gc", Every: 24 * time.Hour, Run: func(ctx context.Context) error { _, err := dictionary.GarbageCollect(ctx, 0); return err }},
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
		if s3, ok := replayStore.(*replay.S3Store); ok {
			jobs = append(jobs, scheduler.Job{Name: "replay-spill-flush", Every: time.Minute, Run: func(ctx context.Context) error {
				return s3.CloseIdleSessions(ctx, clk.Now().Add(-session.IdleTimeout))
			}})
		}
		jobs = append(jobs, scheduler.Job{Name: "replay-migration", Every: time.Minute, Run: func(ctx context.Context) error {
			return runReplayMigration(ctx, pool, replayEnvironment, dataDir, secretKey)
		}})
	}
	if refreshReplayDiskGuard != nil {
		jobs = append(jobs, scheduler.Job{Name: "replay-disk-guard", Every: 30 * time.Second, Run: func(context.Context) error {
			refreshReplayDiskGuard()
			return nil
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

func runReplayMigration(ctx context.Context, pool *pgxpool.Pool, environment config.ReplayEnv, dataDir string, secretKey []byte) error {
	var raw []byte
	if err := pool.QueryRow(ctx, `select settings->'replay_migration' from instance_settings where id`).Scan(&raw); err != nil {
		return fmt.Errorf("load replay migration request: %w", err)
	}
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return nil
	}
	var request struct {
		From json.RawMessage `json:"from"`
		To   json.RawMessage `json:"to"`
	}
	if err := json.Unmarshal(raw, &request); err != nil || len(request.From) == 0 || len(request.To) == 0 {
		return errors.New("invalid replay migration request")
	}
	fromStored, err := replay.OpenStoredStorage(secretKey, request.From)
	if err != nil {
		return fmt.Errorf("open replay migration source settings: %w", err)
	}
	toStored, err := replay.OpenStoredStorage(secretKey, request.To)
	if err != nil {
		return fmt.Errorf("open replay migration destination settings: %w", err)
	}
	from, err := openReplayStore(replay.ResolveStorage(environment, dataDir, fromStored), filepath.Join(dataDir, "replay-migration-from"))
	if err != nil {
		return fmt.Errorf("open replay migration source: %w", err)
	}
	to, err := openReplayStore(replay.ResolveStorage(environment, dataDir, toStored), filepath.Join(dataDir, "replay-migration-to"))
	if err != nil {
		return fmt.Errorf("open replay migration destination: %w", err)
	}
	if err := replay.RunMigration(ctx, pool, from, to); err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `update instance_settings set settings=settings-'replay_migration', updated_at=now() where id`)
	return err
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

func closeDueDays(ctx context.Context, pool *pgxpool.Pool, registry sites.Registry, closed *closedDays, now time.Time, invalidators ...func(int32)) error {
	starts := make(map[int32]time.Time)
	startRows, err := pool.Query(ctx, `
		select id, created_at, timezone
		from sites where deleted_at is null`)
	if err != nil {
		return fmt.Errorf("load site start days: %w", err)
	}
	for startRows.Next() {
		var siteID int32
		var createdAt time.Time
		var timezone string
		if err := startRows.Scan(&siteID, &createdAt, &timezone); err != nil {
			startRows.Close()
			return fmt.Errorf("scan site start day: %w", err)
		}
		starts[siteID] = sites.StartDay(createdAt, timezone)
	}
	if err := startRows.Err(); err != nil {
		startRows.Close()
		return fmt.Errorf("iterate site start days: %w", err)
	}
	startRows.Close()
	due := closer.Due(now, registry, closed.IsClosed, func(siteID int32) time.Time { return starts[siteID] })
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
	days := make([]time.Time, 0, len(due))
	for day := range due {
		days = append(days, day)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
	var failures []error
	for _, day := range days {
		siteIDs := due[day]
		sort.Slice(siteIDs, func(i, j int) bool { return siteIDs[i] < siteIDs[j] })
		seen := make(map[int32]bool, len(siteIDs))
		for _, siteID := range siteIDs {
			if seen[siteID] {
				continue
			}
			seen[siteID] = true
			if _, err := closer.CloseDay(ctx, pool, []int32{siteID}, day); err != nil {
				wrapped := fmt.Errorf("close site %d day %s: %w", siteID, day.Format(time.DateOnly), err)
				slog.Error("close day failed", "site_id", siteID, "day", day.Format(time.DateOnly), "error", err)
				failures = append(failures, wrapped)
				continue
			}
			closed.Mark([]int32{siteID}, day)
			if len(invalidators) > 0 && invalidators[0] != nil {
				invalidators[0](siteID)
			}
		}
	}
	return errors.Join(failures...)
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

	cfg, err := config.LoadLookup(os.LookupEnv)
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
