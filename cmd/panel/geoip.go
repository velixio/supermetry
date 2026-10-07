package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"

	"panel/internal/config"
	"panel/internal/enrich"
	"panel/internal/geoupdate"
	"panel/internal/health"
)

const geoIPUpdateInterval = 24 * time.Hour

type geoUpdateFunc func(context.Context, geoupdate.Config) (geoupdate.Result, error)

type geoService struct {
	mu       sync.Mutex
	cfg      config.Config
	update   geoUpdateFunc
	enricher *enrich.Enricher
	maxMind  bool
}

func prepareGeoIP(ctx context.Context, cfg config.Config, update geoUpdateFunc) (*geoService, error) {
	if update == nil {
		update = geoupdate.Update
	}
	service := &geoService{cfg: cfg, update: update}
	if cfg.MaxMindAccountID == "" {
		loaded, err := enrich.New(cfg.GeoIPPath)
		if err != nil {
			return nil, fmt.Errorf("load required GeoIP database: %w", err)
		}
		service.enricher = loaded
		health.SetGeoIPSource(loaded.GeoIPSource())
		return service, nil
	}

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create GeoIP data directory: %w", err)
	}
	if err := os.Chmod(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("secure GeoIP data directory: %w", err)
	}

	existing := enrich.GeoPaths{Country: cfg.GeoIPPath}
	var missing []string
	for _, edition := range cfg.MaxMindEditions {
		path := filepath.Join(cfg.DataDir, edition+".mmdb")
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				missing = append(missing, edition)
				continue
			}
			return nil, fmt.Errorf("inspect %s database: %w", edition, err)
		}
		if err := validateConfiguredEdition(path, edition); err != nil {
			return nil, err
		}
		setGeoPath(&existing, edition, path)
	}

	// Validate every existing file before touching the network. A corrupt or
	// wrong-edition local database is an operator-visible startup failure.
	probe, err := enrich.NewWithGeo(existing)
	if err != nil {
		return nil, fmt.Errorf("validate required GeoIP database: %w", err)
	}
	if err := probe.Close(); err != nil {
		return nil, fmt.Errorf("close GeoIP validation readers: %w", err)
	}

	for _, edition := range missing {
		_, err := update(ctx, geoupdate.Config{
			Edition: edition, AccountID: cfg.MaxMindAccountID,
			LicenseKey: cfg.MaxMindLicenseKey, DataDir: cfg.DataDir,
		})
		if err != nil {
			return nil, secretSafeError(cfg.MaxMindLicenseKey, fmt.Errorf("download required %s database: %w", edition, err))
		}
	}

	loaded, err := enrich.NewWithGeo(geoPaths(cfg))
	if err != nil {
		return nil, fmt.Errorf("load required MaxMind GeoIP databases: %w", err)
	}
	service.enricher = loaded
	service.maxMind = true
	health.SetGeoIPSource("maxmind")
	if len(missing) > 0 {
		health.GeoIPUpdateLastSuccessUnix.Store(float64(time.Now().Unix()))
	}
	return service, nil
}

func validateConfiguredEdition(path, edition string) error {
	reader, err := maxminddb.Open(path)
	if err != nil {
		return fmt.Errorf("open configured %s database: %w", edition, err)
	}
	databaseType := reader.Metadata.DatabaseType
	verifyErr := reader.Verify()
	closeErr := reader.Close()
	if verifyErr != nil || closeErr != nil {
		return fmt.Errorf("verify configured %s database: %w", edition, errors.Join(verifyErr, closeErr))
	}
	if databaseType != edition {
		return fmt.Errorf("configured %s database has unexpected edition %q", edition, databaseType)
	}
	return nil
}

func (s *geoService) Close() error {
	if s == nil || s.enricher == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enricher.Close()
}

func (s *geoService) Refresh(ctx context.Context, force bool) ([]geoupdate.Result, error) {
	if s == nil || !s.maxMind {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([]geoupdate.Result, 0, len(s.cfg.MaxMindEditions))
	var errs []error
	for _, edition := range s.cfg.MaxMindEditions {
		result, err := s.update(ctx, geoupdate.Config{
			Edition: edition, AccountID: s.cfg.MaxMindAccountID,
			LicenseKey: s.cfg.MaxMindLicenseKey, DataDir: s.cfg.DataDir, Force: force,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("update %s database: %w", edition, err))
			continue
		}
		results = append(results, result)
	}
	if err := errors.Join(errs...); err != nil {
		health.GeoIPUpdateFailures.Add(1)
		return results, secretSafeError(s.cfg.MaxMindLicenseKey, err)
	}
	if err := s.enricher.SwapGeo(geoPaths(s.cfg)); err != nil {
		health.GeoIPUpdateFailures.Add(1)
		return results, fmt.Errorf("activate updated GeoIP databases: %w", err)
	}
	health.GeoIPUpdateLastSuccessUnix.Store(float64(time.Now().Unix()))
	health.SetGeoIPSource("maxmind")
	return results, nil
}

func startGeoIPRefresh(ctx context.Context, service *geoService, report func(error)) <-chan struct{} {
	if service == nil || !service.maxMind {
		return nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := service.Refresh(ctx, false); err != nil && !errors.Is(err, context.Canceled) && report != nil {
			report(err)
		}
	}()
	return done
}

func geoPaths(cfg config.Config) enrich.GeoPaths {
	paths := enrich.GeoPaths{Country: cfg.GeoIPPath}
	for _, edition := range cfg.MaxMindEditions {
		setGeoPath(&paths, edition, filepath.Join(cfg.DataDir, edition+".mmdb"))
	}
	return paths
}

func setGeoPath(paths *enrich.GeoPaths, edition, path string) {
	switch edition {
	case "GeoLite2-City":
		paths.City = path
	case "GeoLite2-ASN":
		paths.ASN = path
	case "GeoLite2-Country":
		paths.Country = path
	}
}

func secretSafeError(secret string, err error) error {
	if err == nil || secret == "" || !strings.Contains(err.Error(), secret) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), secret, "[REDACTED]"))
}

func geoIPCommand(ctx context.Context, args []string, output io.Writer, getenv func(string) string, update geoUpdateFunc) error {
	if len(args) != 1 || args[0] != "update" {
		return errors.New("usage: panel geoip update")
	}
	cfg, err := config.LoadGeoIP(getenv)
	if err != nil {
		return err
	}
	if cfg.MaxMindAccountID == "" {
		return errors.New("panel geoip update requires MaxMind credentials")
	}
	if update == nil {
		update = geoupdate.Update
	}
	for _, edition := range cfg.MaxMindEditions {
		result, err := update(ctx, geoupdate.Config{
			Edition: edition, AccountID: cfg.MaxMindAccountID,
			LicenseKey: cfg.MaxMindLicenseKey, DataDir: cfg.DataDir, Force: true,
		})
		if err != nil {
			return secretSafeError(cfg.MaxMindLicenseKey, fmt.Errorf("update %s database: %w", edition, err))
		}
		fmt.Fprintf(output, "%s\t%s\n", result.Edition, result.ModTime.UTC().Format(time.RFC3339))
	}
	return nil
}
