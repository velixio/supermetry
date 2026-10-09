package main

import (
	"bufio"
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/config"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/enrich"
	"supermetry/internal/privacy"
	"supermetry/internal/salts"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/writer"
)

const loadChunkSize = 4096

type loadStats struct {
	Events   int64         `json:"events"`
	Sessions int64         `json:"sessions"`
	Elapsed  time.Duration `json:"elapsed_ns"`
	PerSec   float64       `json:"events_per_s"`
}

type loadRecord struct {
	TS          time.Time         `json:"ts"`
	Site        int32             `json:"site"`
	Host        string            `json:"host"`
	Path        string            `json:"path"`
	Referrer    string            `json:"ref"`
	UTMSource   string            `json:"utm_source"`
	UTMMedium   string            `json:"utm_medium"`
	UTMCampaign string            `json:"utm_campaign"`
	Name        string            `json:"name"`
	Props       map[string]string `json:"props"`
	IP          string            `json:"ip"`
	UserAgent   string            `json:"ua"`
	ScreenWidth int16             `json:"screen_w"`
	Country     string            `json:"country"`
	Browser     string            `json:"browser"`
	OS          string            `json:"os"`
	Device      string            `json:"device"`
}

type neverClosedDays struct{}

func (neverClosedDays) IsClosed(int32, time.Time) bool { return false }

func loadCommand(ctx context.Context, in io.Reader, out io.Writer) error {
	cfg, err := config.LoadLookup(os.LookupEnv)
	if err != nil {
		return err
	}
	if cfg.SitesFile == "" {
		return errors.New("SUPERMETRY_SITES_FILE is required")
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	registry, err := sites.LoadFile(cfg.SitesFile)
	if err != nil {
		return err
	}
	if err := validateTrackingStartup(ctx, pool, cfg.Tracking, registry.All()); err != nil {
		return err
	}
	stats, err := loadEventsWithPolicy(ctx, pool, registry, in, cfg.QueueSize, cfg.FlushEvery, cfg.SessionFlush, cfg.Tracking.Mode, cfg.TrackingPaths)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(stats)
}

func loadEvents(ctx context.Context, pool *pgxpool.Pool, registry sites.Registry, in io.Reader, modes ...privacy.Mode) (loadStats, error) {
	return loadEventsWithQueue(ctx, pool, registry, in, 1<<16, 20*time.Millisecond, 20*time.Millisecond, modes...)
}

func loadEventsWithQueue(
	ctx context.Context,
	pool *pgxpool.Pool,
	registry sites.Registry,
	in io.Reader,
	queueSize int,
	flushEvery time.Duration,
	sessionFlush time.Duration,
	modes ...privacy.Mode,
) (loadStats, error) {
	mode := privacy.Anonymous
	if len(modes) > 0 {
		mode = modes[0]
	}
	return loadEventsWithPolicy(ctx, pool, registry, in, queueSize, flushEvery, sessionFlush, mode, privacy.PathPolicy{})
}

func loadEventsWithPolicy(ctx context.Context, pool *pgxpool.Pool, registry sites.Registry, in io.Reader, queueSize int, flushEvery, sessionFlush time.Duration, mode privacy.Mode, paths privacy.PathPolicy) (loadStats, error) {
	started := time.Now()
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		return loadStats{}, fmt.Errorf("load dictionary: %w", err)
	}
	sourceClassifier, err := enrich.NewSourceClassifier()
	if err != nil {
		return loadStats{}, fmt.Errorf("load source classifier: %w", err)
	}
	sessions := session.NewMemory(16, nil)
	w := writer.New(pool, sessions, queueSize, flushEvery, sessionFlush, neverClosedDays{})
	w.SetTrackingPolicy(mode, registry)
	// Imported records have no collection-time provenance; the writer must not
	// promote historical paths merely because they match today's allowlist.
	w.SetPathPolicy(paths, dictionary)
	writerCtx, stopWriter := context.WithCancel(context.Background())
	writerDone := make(chan error, 1)
	go func() { writerDone <- w.Run(writerCtx) }()

	loader := &ndjsonLoader{
		pool: pool, registry: registry, dictionary: dictionary, sessions: sessions, writer: w,
		sourceClassifier: sourceClassifier, salts: make(map[string][]byte),
	}
	events, loadErr := loader.run(ctx, in)
	stopWriter()
	writeErr := <-writerDone
	if loadErr != nil || writeErr != nil {
		return loadStats{}, errors.Join(loadErr, writeErr)
	}

	var sessionCount int64
	if err := pool.QueryRow(ctx, `select count(*) from sessions`).Scan(&sessionCount); err != nil {
		return loadStats{}, fmt.Errorf("count loaded sessions: %w", err)
	}
	elapsed := time.Since(started)
	return loadStats{
		Events: events, Sessions: sessionCount, Elapsed: elapsed,
		PerSec: float64(events) / max(elapsed.Seconds(), 0.000001),
	}, nil
}

type ndjsonLoader struct {
	pool             *pgxpool.Pool
	registry         sites.Registry
	dictionary       *dict.Dict
	sessions         session.Store
	writer           *writer.Writer
	sourceClassifier *enrich.SourceClassifier
	salts            map[string][]byte
	ensuredTo        time.Time
	lastTS           time.Time
}

func (l *ndjsonLoader) run(ctx context.Context, in io.Reader) (int64, error) {
	reader := bufio.NewReaderSize(in, 1<<20)
	chunk := make([]loadRecord, 0, loadChunkSize)
	var loaded, line int64
	for {
		data, err := reader.ReadBytes('\n')
		if len(data) != 0 {
			line++
			var record loadRecord
			if decodeErr := json.Unmarshal(data, &record); decodeErr != nil {
				return loaded, fmt.Errorf("line %d: invalid event", line)
			}
			if record.TS.IsZero() || record.TS.Before(l.lastTS) {
				return loaded, fmt.Errorf("line %d: events must have ordered timestamps", line)
			}
			l.lastTS = record.TS
			chunk = append(chunk, record)
			if len(chunk) == cap(chunk) {
				if loadErr := l.loadChunk(ctx, chunk); loadErr != nil {
					return loaded, fmt.Errorf("line %d: %w", line-int64(len(chunk))+1, loadErr)
				}
				loaded += int64(len(chunk))
				clear(chunk)
				chunk = chunk[:0]
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return loaded, fmt.Errorf("read input: %w", err)
		}
	}
	if len(chunk) != 0 {
		if err := l.loadChunk(ctx, chunk); err != nil {
			return loaded, fmt.Errorf("line %d: %w", line-int64(len(chunk))+1, err)
		}
		loaded += int64(len(chunk))
	}
	return loaded, nil
}

func (l *ndjsonLoader) loadChunk(ctx context.Context, records []loadRecord) error {
	if err := l.ensurePartitions(ctx, records[0].TS, records[len(records)-1].TS); err != nil {
		return err
	}
	dimensions, err := l.dimensions(ctx, records)
	if err != nil {
		return err
	}
	for i := range records {
		record := &records[i]
		site, ok := l.registry.Site(record.Site)
		if !ok || site.TZ == nil || !site.Allows(strings.ToLower(record.Host)) {
			return errors.New("unknown site")
		}
		if record.Name == "" || record.IP == "" || record.UserAgent == "" || len(record.Country) != 2 {
			return errors.New("invalid event")
		}

		localDay := salts.LocalDay(record.TS, site.TZ)
		visitorID, err := l.visitorID(site.ID, localDay, record.IP, record.UserAgent)
		if err != nil {
			return err
		}
		var previousVisitorID int64
		localNow := record.TS.In(site.TZ)
		midnight := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, site.TZ)
		if localNow.Sub(midnight) <= 30*time.Minute {
			previousVisitorID, err = l.visitorID(site.ID, localDay.AddDate(0, 0, -1), record.IP, record.UserAgent)
			if err != nil {
				return err
			}
		}

		kind := int16(1)
		nameID := dimensions.names[i]
		if record.Name == "pageview" {
			kind = 0
			nameID = 0
		}
		hit := session.Hit{
			SiteID: site.ID, Retention: site.Retention, TS: record.TS, LocalDay: localDay,
			VisitorID: visitorID, PrevVisitorID: previousVisitorID, Kind: kind,
			PathID: dimensions.paths[i], NameID: nameID, Source: dimensions.sources[i], UTMSource: dimensions.utmSources[i],
			Medium: dimensions.mediums[i], Campaign: dimensions.campaigns[i],
			Country: record.Country, Browser: dimensions.browsers[i], OS: dimensions.operatingSystems[i],
			Device: loadDevice(record.Device),
		}
		result := l.sessions.Touch(hit)
		hit.VisitorID = result.VisitorID
		event := writer.Event{
			Hit: hit, SessionID: result.SessionID, Screen: record.ScreenWidth,
			ReferrerID: dimensions.referrers[i],
		}
		if kind == 1 {
			event.Props = record.Props
		}
		for !l.writer.Enqueue(event) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Millisecond):
			}
		}
	}
	l.sessions.Expire(records[len(records)-1].TS)
	return nil
}

func (l *ndjsonLoader) ensurePartitions(ctx context.Context, first, last time.Time) error {
	if !l.ensuredTo.IsZero() && last.Before(l.ensuredTo) {
		return nil
	}
	from := first.AddDate(0, 0, -1)
	if !l.ensuredTo.IsZero() {
		from = l.ensuredTo.AddDate(0, 0, -1)
	}
	to := last.AddDate(0, 0, 8)
	if err := db.EnsurePartitions(ctx, l.pool, from, to); err != nil {
		return fmt.Errorf("ensure load partitions: %w", err)
	}
	l.ensuredTo = to
	return nil
}

type loadDimensions struct {
	paths, referrers, sources, utmSources, mediums, campaigns, names, browsers, operatingSystems []int32
}

func (l *ndjsonLoader) dimensions(ctx context.Context, records []loadRecord) (loadDimensions, error) {
	values := func(get func(loadRecord) string) []string {
		out := make([]string, len(records))
		for i, record := range records {
			out[i] = get(record)
		}
		return out
	}
	sources := make([]string, len(records))
	mediums := make([]string, len(records))
	for i, record := range records {
		sources[i], mediums[i] = l.sourceClassifier.Source(
			record.Referrer,
			"https://"+record.Host+"/",
			enrich.UTM{Source: record.UTMSource, Medium: record.UTMMedium},
		)
		if sources[i] == "" {
			sources[i] = "(direct)"
		}
	}
	lookups := []struct {
		kind   dict.Kind
		values []string
		to     *[]int32
	}{
		{dict.Path, values(func(r loadRecord) string { return r.Path }), nil},
		{dict.Referrer, values(func(r loadRecord) string { return r.Referrer }), nil},
		{dict.Source, sources, nil},
		{dict.Source, values(func(r loadRecord) string { return r.UTMSource }), nil},
		{dict.Medium, mediums, nil},
		{dict.Campaign, values(func(r loadRecord) string { return r.UTMCampaign }), nil},
		{dict.EventName, values(func(r loadRecord) string { return r.Name }), nil},
		{dict.Browser, values(func(r loadRecord) string { return r.Browser }), nil},
		{dict.OS, values(func(r loadRecord) string { return r.OS }), nil},
	}
	dimensions := loadDimensions{}
	lookups[0].to = &dimensions.paths
	lookups[1].to = &dimensions.referrers
	lookups[2].to = &dimensions.sources
	lookups[3].to = &dimensions.utmSources
	lookups[4].to = &dimensions.mediums
	lookups[5].to = &dimensions.campaigns
	lookups[6].to = &dimensions.names
	lookups[7].to = &dimensions.browsers
	lookups[8].to = &dimensions.operatingSystems
	for _, lookup := range lookups {
		ids, err := dimensionIDs(ctx, l.dictionary, lookup.kind, lookup.values)
		if err != nil {
			return loadDimensions{}, fmt.Errorf("encode event dimensions: %w", err)
		}
		*lookup.to = ids
	}
	return dimensions, nil
}

func dimensionIDs(ctx context.Context, dictionary *dict.Dict, kind dict.Kind, values []string) ([]int32, error) {
	ids := make([]int32, len(values))
	nonempty := make([]string, 0, len(values))
	positions := make([]int, 0, len(values))
	for i, value := range values {
		if value != "" {
			nonempty = append(nonempty, value)
			positions = append(positions, i)
		}
	}
	encoded, err := dictionary.IDs(ctx, kind, nonempty)
	if err != nil {
		return nil, err
	}
	for i, position := range positions {
		ids[position] = encoded[i]
	}
	return ids, nil
}

func (l *ndjsonLoader) visitorID(siteID int32, day time.Time, ip, userAgent string) (int64, error) {
	key := day.Format(time.DateOnly)
	salt := l.salts[key]
	if salt == nil {
		salt = make([]byte, 32)
		if _, err := cryptorand.Read(salt); err != nil {
			return 0, errors.New("generate load salt")
		}
		l.salts[key] = salt
	}
	return salts.VisitorID(salt, siteID, ip, userAgent), nil
}

func loadDevice(value string) int16 {
	switch value {
	case "mobile":
		return 1
	case "tablet":
		return 2
	default:
		return 0
	}
}
