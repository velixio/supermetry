package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"

	"panel/internal/config"
	"panel/internal/geoupdate"
	"panel/internal/health"
)

func TestServeMaxMindDownloadFailurePreventsListening(t *testing.T) {
	const licenseKey = "do-not-disclose"
	listenCalls := 0
	cfg := config.Config{
		MaxMindAccountID: "42", MaxMindLicenseKey: licenseKey,
		MaxMindEditions: []string{"GeoLite2-City"}, DataDir: t.TempDir(),
	}
	err := serve(context.Background(), cfg,
		withGeoUpdate(func(context.Context, geoupdate.Config) (geoupdate.Result, error) {
			return geoupdate.Result{}, errors.New("download refused")
		}),
		withListen(func(string, string) (net.Listener, error) {
			listenCalls++
			return nil, errors.New("unexpected listen")
		}),
	)
	if err == nil || !strings.Contains(err.Error(), "download refused") {
		t.Fatalf("serve() error = %v", err)
	}
	if strings.Contains(err.Error(), licenseKey) {
		t.Fatalf("serve() error leaked license key: %v", err)
	}
	if listenCalls != 0 {
		t.Fatalf("listen calls = %d, want 0", listenCalls)
	}
}

func TestServeCorruptMaxMindDatabasePreventsListening(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "GeoLite2-City.mmdb"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	updateCalls, listenCalls := 0, 0
	err := serve(context.Background(), config.Config{
		MaxMindAccountID: "42", MaxMindLicenseKey: "secret",
		MaxMindEditions: []string{"GeoLite2-City"}, DataDir: dataDir,
	},
		withGeoUpdate(func(context.Context, geoupdate.Config) (geoupdate.Result, error) {
			updateCalls++
			return geoupdate.Result{}, nil
		}),
		withListen(func(string, string) (net.Listener, error) {
			listenCalls++
			return nil, errors.New("unexpected listen")
		}),
	)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "database") {
		t.Fatalf("serve() error = %v, want database validation error", err)
	}
	if updateCalls != 0 || listenCalls != 0 {
		t.Fatalf("update/listen calls = %d/%d, want 0/0", updateCalls, listenCalls)
	}
}

func TestPrepareGeoIPRejectsUnexpectedExactEdition(t *testing.T) {
	dataDir := t.TempDir()
	writeCommandMMDB(t, filepath.Join(dataDir, "GeoLite2-City.mmdb"), "GeoIP2-City", "US")
	service, err := prepareGeoIP(context.Background(), config.Config{
		MaxMindAccountID: "42", MaxMindLicenseKey: "secret",
		MaxMindEditions: []string{"GeoLite2-City"}, DataDir: dataDir,
	}, func(context.Context, geoupdate.Config) (geoupdate.Result, error) {
		t.Fatal("network update called for wrong-edition local database")
		return geoupdate.Result{}, nil
	})
	if service != nil {
		_ = service.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "GeoIP2-City") || !strings.Contains(err.Error(), "GeoLite2-City") {
		t.Fatalf("prepareGeoIP() error = %v, want exact-edition mismatch", err)
	}
}

func TestPrepareGeoIPUsesValidFilesWithoutNetwork(t *testing.T) {
	dataDir := t.TempDir()
	writeCommandMMDB(t, filepath.Join(dataDir, "GeoLite2-City.mmdb"), "GeoLite2-City", "US")
	service, err := prepareGeoIP(context.Background(), config.Config{
		MaxMindAccountID: "42", MaxMindLicenseKey: "secret",
		MaxMindEditions: []string{"GeoLite2-City"}, DataDir: dataDir,
	}, func(context.Context, geoupdate.Config) (geoupdate.Result, error) {
		t.Fatal("network update called with a valid local database")
		return geoupdate.Result{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	if got := service.enricher.Country(netip.MustParseAddr("192.0.2.42")); got != "US" {
		t.Fatalf("Country() = %q, want US", got)
	}
}

func TestStartGeoIPRefreshDoesNotDelayStartup(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "GeoLite2-City.mmdb")
	writeCommandMMDB(t, path, "GeoLite2-City", "US")
	entered := make(chan struct{})
	service, err := prepareGeoIP(context.Background(), config.Config{
		MaxMindAccountID: "42", MaxMindLicenseKey: "secret",
		MaxMindEditions: []string{"GeoLite2-City"}, DataDir: dataDir,
	}, func(ctx context.Context, _ geoupdate.Config) (geoupdate.Result, error) {
		close(entered)
		<-ctx.Done()
		return geoupdate.Result{}, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	done := startGeoIPRefresh(ctx, service, func(error) {})
	if done == nil {
		t.Fatal("background refresh was not started")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("background refresh did not enter updater")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("background refresh did not stop after cancellation")
	}
}

func TestGeoIPRefreshSwapsReaders(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "GeoLite2-City.mmdb")
	writeCommandMMDB(t, path, "GeoLite2-City", "US")
	service, err := prepareGeoIP(context.Background(), config.Config{
		MaxMindAccountID: "42", MaxMindLicenseKey: "secret",
		MaxMindEditions: []string{"GeoLite2-City"}, DataDir: dataDir,
	}, func(context.Context, geoupdate.Config) (geoupdate.Result, error) {
		replacement := filepath.Join(dataDir, "replacement.mmdb")
		writeCommandMMDB(t, replacement, "GeoLite2-City", "GB")
		if err := os.Rename(replacement, path); err != nil {
			t.Fatal(err)
		}
		return geoupdate.Result{Edition: "GeoLite2-City", Path: path, Updated: true, ModTime: time.Now()}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	if _, err := service.Refresh(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if got := service.enricher.Country(netip.MustParseAddr("192.0.2.42")); got != "GB" {
		t.Fatalf("Country() after refresh = %q, want GB", got)
	}
}

func TestGeoIPRefreshFailureKeepsReadersAndCountsFailure(t *testing.T) {
	const key = "hidden-license-key"
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "GeoLite2-City.mmdb")
	writeCommandMMDB(t, path, "GeoLite2-City", "US")
	service, err := prepareGeoIP(context.Background(), config.Config{
		MaxMindAccountID: "42", MaxMindLicenseKey: key,
		MaxMindEditions: []string{"GeoLite2-City"}, DataDir: dataDir,
	}, func(context.Context, geoupdate.Config) (geoupdate.Result, error) {
		return geoupdate.Result{}, errors.New("rejected " + key)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	before := health.GeoIPUpdateFailures.Load()
	_, err = service.Refresh(context.Background(), false)
	if err == nil || strings.Contains(err.Error(), key) {
		t.Fatalf("Refresh() error = %v", err)
	}
	if got := service.enricher.Country(netip.MustParseAddr("192.0.2.42")); got != "US" {
		t.Fatalf("Country() after failed refresh = %q, want US", got)
	}
	if got := health.GeoIPUpdateFailures.Load(); got != before+1 {
		t.Fatalf("failure counter = %d, want %d", got, before+1)
	}
}

func TestGeoIPRefreshSerializesConcurrentRuns(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "GeoLite2-City.mmdb")
	writeCommandMMDB(t, path, "GeoLite2-City", "US")
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	service, err := prepareGeoIP(context.Background(), config.Config{
		MaxMindAccountID: "42", MaxMindLicenseKey: "secret",
		MaxMindEditions: []string{"GeoLite2-City"}, DataDir: dataDir,
	}, func(context.Context, geoupdate.Config) (geoupdate.Result, error) {
		entered <- struct{}{}
		<-release
		return geoupdate.Result{Edition: "GeoLite2-City", Path: path, ModTime: time.Now()}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })

	var runs sync.WaitGroup
	for range 2 {
		runs.Go(func() {
			if _, err := service.Refresh(context.Background(), false); err != nil {
				t.Errorf("Refresh() error = %v", err)
			}
		})
	}
	<-entered
	select {
	case <-entered:
		close(release)
		runs.Wait()
		t.Fatal("concurrent refresh entered updater before the first completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	runs.Wait()
}

func TestScheduledJobsRegistersGeoIPUpdate(t *testing.T) {
	service := &geoService{maxMind: true}
	jobs := scheduledJobs(nil, nil, nil, nil, nil, nil, nil, nil, nil, service, wallClock{})
	for _, job := range jobs {
		if job.Name == "geoip-update" {
			if job.Every != 24*time.Hour {
				t.Fatalf("geoip-update interval = %v, want 24h", job.Every)
			}
			return
		}
	}
	t.Fatal("geoip-update job not registered")
}

func TestGeoIPCommandForcesUpdateAndPrintsEditions(t *testing.T) {
	values := map[string]string{
		"PANEL_MAXMIND_ACCOUNT_ID":  "42",
		"PANEL_MAXMIND_LICENSE_KEY": "secret-value",
		"PANEL_MAXMIND_EDITIONS":    "GeoLite2-City,GeoLite2-ASN",
		"PANEL_DATA_DIR":            t.TempDir(),
	}
	var output bytes.Buffer
	var editions []string
	err := geoIPCommand(context.Background(), []string{"update"}, &output, func(key string) string { return values[key] }, func(_ context.Context, cfg geoupdate.Config) (geoupdate.Result, error) {
		if !cfg.Force {
			t.Error("CLI update was not forced")
		}
		editions = append(editions, cfg.Edition)
		return geoupdate.Result{Edition: cfg.Edition, Path: filepath.Join(cfg.DataDir, cfg.Edition+".mmdb"), ModTime: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), Updated: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(editions, ","); got != "GeoLite2-City,GeoLite2-ASN" {
		t.Fatalf("editions = %q", got)
	}
	text := output.String()
	if !strings.Contains(text, "GeoLite2-City") || !strings.Contains(text, "2026-10-08") || strings.Contains(text, values["PANEL_MAXMIND_LICENSE_KEY"]) {
		t.Fatalf("output = %q", text)
	}
}

func TestGeoIPCommandRedactsLicenseKeyFromFailure(t *testing.T) {
	const key = "secret-value"
	values := map[string]string{
		"PANEL_MAXMIND_ACCOUNT_ID":  "42",
		"PANEL_MAXMIND_LICENSE_KEY": key,
		"PANEL_MAXMIND_EDITIONS":    "GeoLite2-City",
		"PANEL_DATA_DIR":            t.TempDir(),
	}
	err := geoIPCommand(context.Background(), []string{"update"}, io.Discard, func(name string) string { return values[name] }, func(context.Context, geoupdate.Config) (geoupdate.Result, error) {
		return geoupdate.Result{}, errors.New("service rejected " + key)
	})
	if err == nil || strings.Contains(err.Error(), key) {
		t.Fatalf("geoIPCommand() error = %v", err)
	}
}

func writeCommandMMDB(t *testing.T, path, edition, country string) {
	t.Helper()
	tree, err := mmdbwriter.New(mmdbwriter.Options{
		BuildEpoch: 1_700_000_000, DatabaseType: edition,
		Description:             map[string]string{"en": "Panel command test database"},
		IncludeReservedNetworks: true, IPVersion: 4, Languages: []string{"en"}, RecordSize: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, network, err := net.ParseCIDR("192.0.2.0/24")
	if err != nil {
		t.Fatal(err)
	}
	record := mmdbtype.Map{
		"country": mmdbtype.Map{
			"iso_code": mmdbtype.String(country),
			"names":    mmdbtype.Map{"en": mmdbtype.String(country)},
		},
		"city": mmdbtype.Map{"names": mmdbtype.Map{"en": mmdbtype.String("Test City")}},
	}
	if err := tree.Insert(network, record); err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	if _, err := tree.WriteTo(&data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}
