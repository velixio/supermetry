package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/api"
	"supermetry/internal/config"
	"supermetry/internal/dict"
	"supermetry/internal/heatmap"
	"supermetry/internal/ingest"
	"supermetry/internal/minutes"
	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/authn"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	platformgoals "supermetry/internal/platform/goals"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/keys"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/orgs"
	"supermetry/internal/platform/shares"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/privacy"
	analyticsquery "supermetry/internal/query"
	"supermetry/internal/session"
)

const benchmarkDatabase = "supermetry_bench"

type datasetTier struct {
	Name          string
	GeneratorTier string
	Days          int
	EventsPerDay  int
	Sites         int
}

func tierConfig(mode string) datasetTier {
	if mode == "full" {
		return datasetTier{Name: "M", GeneratorTier: "M", Days: 14, EventsPerDay: 1_000_000, Sites: 100}
	}
	// The generator's Zipf distribution gives site 1 about 30% of the stream:
	// 11,112/day for 30 days therefore yields about 100K events on the S site.
	// Fifty platform sites exercise list routes without changing generator shape.
	return datasetTier{Name: "S+many-sites", GeneratorTier: "S", Days: 30, EventsPerDay: 11_112, Sites: 50}
}

type siteInfo struct {
	ID            int32    `json:"id"`
	Host          string   `json:"host"`
	Timezone      string   `json:"timezone"`
	OrgID         int64    `json:"org_id"`
	PublicID      string   `json:"public_id"`
	RetentionDays int16    `json:"retention_days"`
	AllowedHosts  []string `json:"allowed_hosts"`
}

type benchmarkEnv struct {
	Pool       *pgxpool.Pool
	Handler    http.Handler
	APIKey     string
	Session    string
	OrgID      int64
	SiteID     int32
	PublicID   string
	closeFuncs []func()
}

type disabledMail struct{}

func (disabledMail) Send(context.Context, mail.Message) error { return nil }
func (disabledMail) Enabled() bool                            { return false }

// benchmarkNow is the fixed clock of every benchmark run, so closed and open
// days are the same on every run.
var benchmarkNow = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

type fixedClock time.Time

func (c fixedClock) Now() time.Time { return time.Time(c) }

func resetBenchmarkDatabase(ctx context.Context, dsn string) error {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return errors.New("parse benchmark database URL")
	}
	if config.Database != benchmarkDatabase {
		return fmt.Errorf("refusing database %q: benchmark requires %q", config.Database, benchmarkDatabase)
	}
	config.Database = "postgres"
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return errors.New("connect to PostgreSQL for benchmark reset")
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `select pg_terminate_backend(pid) from pg_stat_activity where datname=$1 and pid <> pg_backend_pid()`, benchmarkDatabase); err != nil {
		return fmt.Errorf("terminate benchmark database sessions: %w", err)
	}
	identifier := pgx.Identifier{benchmarkDatabase}.Sanitize()
	if _, err := conn.Exec(ctx, "drop database if exists "+identifier); err != nil {
		return fmt.Errorf("drop benchmark database: %w", err)
	}
	if _, err := conn.Exec(ctx, "create database "+identifier); err != nil {
		return fmt.Errorf("create benchmark database: %w", err)
	}
	return nil
}

func generateDataset(ctx context.Context, tier datasetTier, seed uint64, outputDir, generatorBin string) (string, string, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", "", err
	}
	eventsPath := filepath.Join(outputDir, "events.ndjson")
	sitesPath := filepath.Join(outputDir, "sites.json")
	truthPath := filepath.Join(outputDir, "truth.json")
	command := exec.CommandContext(ctx, generatorBin,
		"--tier", tier.GeneratorTier, "--days", strconv.Itoa(tier.Days), "--seed", strconv.FormatUint(seed, 10),
		"--events-per-day", strconv.Itoa(tier.EventsPerDay), "--out", eventsPath, "--sites", sitesPath, "--truth", truthPath, "--quiet")
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		return "", "", fmt.Errorf("generate dataset: %w", err)
	}
	if err := enrichGeneratedSites(sitesPath); err != nil {
		return "", "", err
	}
	return eventsPath, sitesPath, nil
}

func enrichGeneratedSites(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	var sites []siteInfo
	err = json.NewDecoder(file).Decode(&sites)
	file.Close()
	if err != nil {
		return fmt.Errorf("decode generated sites: %w", err)
	}
	for index := range sites {
		sites[index].OrgID = 1
		sites[index].PublicID = fmt.Sprintf("bench_%03d", sites[index].ID)
		sites[index].RetentionDays = 30
		sites[index].AllowedHosts = []string{sites[index].Host}
	}
	file, err = os.Create(path)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(sites); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func openPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("parse benchmark database URL")
	}
	if config.ConnConfig.Database != benchmarkDatabase {
		return nil, fmt.Errorf("refusing database %q: benchmark requires %q", config.ConnConfig.Database, benchmarkDatabase)
	}
	configureTrace(config)
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open benchmark database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("ping benchmark database")
	}
	return pool, nil
}

func seedPlatform(ctx context.Context, pool *pgxpool.Pool, sitesPath string, limit int) (int64, int64, []siteInfo, error) {
	file, err := os.Open(sitesPath)
	if err != nil {
		return 0, 0, nil, err
	}
	defer file.Close()
	var generated []siteInfo
	if err := json.NewDecoder(file).Decode(&generated); err != nil {
		return 0, 0, nil, fmt.Errorf("decode generated sites: %w", err)
	}
	if limit < len(generated) {
		generated = generated[:limit]
	}
	var userID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('benchmark@example.test','Benchmark','disabled') returning id`).Scan(&userID); err != nil {
		return 0, 0, nil, fmt.Errorf("seed benchmark user: %w", err)
	}
	data := &store.Store{Pool: pool}
	if err := data.Tx(ctx, func(tx pgx.Tx) error {
		var createErr error
		orgID, createErr = store.CreateOrg(ctx, tx, "Benchmark", userID)
		return createErr
	}); err != nil {
		return 0, 0, nil, fmt.Errorf("seed benchmark organization: %w", err)
	}
	registry := sitesvc.NewRegistry(data, 0)
	defer registry.Close()
	siteService := sitesvc.New(data, registry, privacy.Identified)
	principal := authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}
	for _, generatedSite := range generated {
		created, err := siteService.Create(ctx, principal, "Benchmark "+strconv.Itoa(int(generatedSite.ID)), []string{generatedSite.Host}, generatedSite.Timezone, 30, "USD")
		if err != nil {
			return 0, 0, nil, fmt.Errorf("seed benchmark site %d: %w", generatedSite.ID, err)
		}
		if created.ID != generatedSite.ID {
			return 0, 0, nil, fmt.Errorf("generated site id %d mapped to %d", generatedSite.ID, created.ID)
		}
	}
	return userID, orgID, generated, nil
}

func loadDataset(ctx context.Context, dsn, sitesPath, eventsPath, serverBin string) error {
	events, err := os.Open(eventsPath)
	if err != nil {
		return err
	}
	defer events.Close()
	command := exec.CommandContext(ctx, serverBin, "load")
	command.Stdin = events
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	command.Env = append(os.Environ(),
		"SUPERMETRY_DATABASE_URL="+dsn,
		"SUPERMETRY_SITES_FILE="+sitesPath,
		"SUPERMETRY_BASE_URL=http://127.0.0.1",
		"SUPERMETRY_SECRET_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		"SUPERMETRY_TRACKING_MODE=anonymous",
	)
	if err := command.Run(); err != nil {
		return fmt.Errorf("load benchmark dataset: %w", err)
	}
	return nil
}

func newBenchmarkEnv(ctx context.Context, pool *pgxpool.Pool, userID, orgID int64) (*benchmarkEnv, error) {
	now := benchmarkNow
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, 0)
	keyService := keys.New(data)
	definitions := platformgoals.New(data, 0)
	orgService := orgs.New(data, registry)
	siteService := sitesvc.New(data, registry, privacy.Identified)
	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "supermetry/totp/v1")
	if err != nil {
		return nil, err
	}
	authService := platformauth.New(data, disabledMail{}, box, "http://127.0.0.1", platformauth.WithClock(fixedClock(now)))
	shareService := shares.New(data)
	analyticsAuthenticator := authn.New(authService, keyService, shareService, orgService)
	platformAuthenticator := platformhttp.NewAuthenticator(authService, keyService, shareService, orgService)
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		return nil, fmt.Errorf("load dictionary: %w", err)
	}
	engine := analyticsquery.NewEngine(pool, dictionary, registry, definitions)
	engine.SetTrackingMode(privacy.Identified)
	live := session.NewMemory(4, fixedClock(now))
	platform := platformhttp.New(platformhttp.Deps{
		Now: func() time.Time { return now }, TrackingMode: privacy.Identified,
		BaseURL: "http://127.0.0.1", TrackerPath: "/js/script.js",
		Authenticator: platformAuthenticator, Sites: registry, Store: data, Auth: authService,
		Orgs: orgService, Keys: keyService, Definitions: definitions,
		Mutations: platformhttp.Services{Sites: siteService, Keys: keyService, Store: data},
	})
	router := api.NewRouter(api.Deps{
		Config: config.Config{EventPath: "/api/public/v1/events", TrackerPath: "/js/script.js", Tracking: config.TrackingMode{Mode: privacy.Identified}},
		Query:  engine, SessionExplore: engine, Heatmaps: heatmap.New(pool, registry),
		PublicAuth: analyticsAuthenticator, PrivateAuth: analyticsAuthenticator, Sites: registry,
		Platform: platform, Sessions: live, MinuteSource: minutes.NewStore(pool), Dictionary: dictionary,
		Now: func() time.Time { return now }, RateLimitNow: time.Now,
		RateLimiter: ingest.NewTokenBuckets(1_000_000, 1_000_000),
		Ingest:      http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }),
	})
	if err := registry.Invalidate(); err != nil {
		return nil, fmt.Errorf("refresh sites: %w", err)
	}
	scopes := []string{"stats:read", "profiles:read", "replay:read", "data:export", "sites:write", "goals:write", "segments:write", "annotations:write", "keys:manage"}
	rows, err := pool.Query(ctx, `select id from sites where org_id=$1 order by id`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list benchmark sites: %w", err)
	}
	var siteIDs []int32
	for rows.Next() {
		var siteID int32
		if err := rows.Scan(&siteID); err != nil {
			rows.Close()
			return nil, err
		}
		siteIDs = append(siteIDs, siteID)
	}
	rows.Close()
	apiKey, _, err := keyService.Create(ctx, authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}, "Benchmark", scopes, siteIDs, nil)
	if err != nil {
		return nil, fmt.Errorf("seed benchmark API key: %w", err)
	}
	sessionToken := "benchmark-session-token"
	if err := store.CreateSession(ctx, pool, store.UserSession{IDHash: platformcrypto.HashToken(sessionToken), UserID: userID, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour), DeviceLabel: "Benchmark"}); err != nil {
		return nil, fmt.Errorf("seed benchmark session: %w", err)
	}
	var publicID string
	if err := pool.QueryRow(ctx, `select public_id from sites where id=1`).Scan(&publicID); err != nil {
		return nil, fmt.Errorf("read benchmark site: %w", err)
	}
	env := &benchmarkEnv{Pool: pool, Handler: traceMiddleware(router), APIKey: apiKey, Session: sessionToken, OrgID: orgID, SiteID: 1, PublicID: publicID}
	env.closeFuncs = []func(){definitions.Close, keyService.Close, registry.Close, func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = authService.Shutdown(shutdownCtx)
	}}
	return env, nil
}

func (e *benchmarkEnv) Close() {
	for i := len(e.closeFuncs) - 1; i >= 0; i-- {
		e.closeFuncs[i]()
	}
}
