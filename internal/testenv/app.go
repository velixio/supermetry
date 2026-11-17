package testenv

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/appingest"
	"supermetry/internal/sentryingest"
	"supermetry/internal/sites"
)

const (
	appSiteID = int32(42)
	appDSNKey = "0123456789abcdef0123456789abcdef"
)

// appNow is the clock of the Sentry intake; the recorded fixtures are dated
// the same day.
var appNow = time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC)

// app is one app project behind the real Sentry envelope handler, body writer
// and client-report aggregator.
type app struct {
	handler *sentryingest.Handler
	cancel  context.CancelFunc
	done    []chan error
	once    sync.Once
}

// appRegistry resolves the one DSN key of the test app project.
type appRegistry struct {
	*registry
	site sites.Site
}

func (r appRegistry) Site(id int32) (sites.Site, bool) { return r.site, id == r.site.ID }
func (r appRegistry) ByDSNKey(k string) (sites.Site, sites.DSNKey, bool) {
	for _, dk := range r.site.App.DSNKeys {
		if dk.PublicKey == k {
			return r.site, dk, true
		}
	}
	return sites.Site{}, sites.DSNKey{}, false
}

func startApp(t *testing.T, pool *pgxpool.Pool) *app {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('App Org','app-org')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values($1,(select id from orgs where slug='app-org'),'appsite00042','App','UTC',30,'app')`, appSiteID); err != nil {
		t.Fatal(err)
	}
	site := sites.Site{ID: appSiteID, Kind: sites.KindApp, TZ: time.UTC, App: &sites.AppConfig{
		DSNKeys: []sites.DSNKey{{PublicKey: appDSNKey}}, MaxReportAgeDays: 7, HashSecret: []byte("0123456789abcdef0123456789abcdef"),
	}}
	intake := appingest.NewIntake(appingest.Config{})
	writer := appingest.NewWriter(pool, intake, appingest.WriterConfig{FlushEvery: 10 * time.Millisecond})
	bodies := appingest.NewBodyWriter(pool, appingest.BodyWriterConfig{})
	reports := appingest.NewClientReports(pool, 0)
	handler := sentryingest.NewHandler(appRegistry{site: site}, intake, bodies, nil, nil, func() time.Time { return appNow }).
		WithClientReports(reports).WithIPLimit(-1)
	runCtx, cancel := context.WithCancel(ctx)
	a := &app{handler: handler, cancel: cancel}
	for _, run := range []func(context.Context) error{writer.Run, bodies.Run, reports.Run} {
		done := make(chan error, 1)
		a.done = append(a.done, done)
		go func() { done <- run(runCtx) }()
	}
	t.Cleanup(a.stop)
	return a
}

// stop drains the app writers; it is safe on a nil app and to call twice.
func (a *app) stop() {
	if a == nil {
		return
	}
	a.once.Do(func() {
		a.cancel()
		for _, done := range a.done {
			<-done
		}
	})
}

// StartWithApp is Start plus an app project (site 42) whose Sentry envelope
// endpoint is mounted on the server.
func StartWithApp(t *testing.T) *Env { return start(t, true) }

// AppFixture is one recorded Sentry envelope as the SDK sent it.
type AppFixture struct {
	Name    string
	Raw     []byte // the request body as sent (possibly compressed)
	Plain   []byte // the body after content decoding
	Method  string
	Path    string
	Query   string
	Headers map[string]string
}

// LoadAppFixtures reads the recorded SDK envelopes under tools/sentry-fixtures'
// corpus (internal/sentryingest/testdata).
func LoadAppFixtures(t *testing.T) []AppFixture {
	t.Helper()
	dir := filepath.Join(repositoryRoot, "internal", "sentryingest", "testdata")
	files, err := filepath.Glob(filepath.Join(dir, "*", "*.envelope"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no sentry fixtures under %s: %v", dir, err)
	}
	var out []AppFixture
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		side, err := os.ReadFile(strings.TrimSuffix(file, ".envelope") + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var sc struct {
			Method  string            `json:"method"`
			Path    string            `json:"path"`
			Query   string            `json:"query"`
			Headers map[string]string `json:"headers"`
		}
		if err := json.Unmarshal(side, &sc); err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(dir, file)
		f := AppFixture{
			Name: strings.TrimSuffix(filepath.ToSlash(rel), ".envelope"), Raw: raw, Plain: raw,
			Method: sc.Method, Path: sc.Path, Query: sc.Query, Headers: sc.Headers,
		}
		for k, v := range sc.Headers {
			if strings.EqualFold(k, "Content-Encoding") && strings.EqualFold(v, "gzip") {
				if zr, err := gzip.NewReader(bytes.NewReader(raw)); err == nil {
					if b, err := io.ReadAll(zr); err == nil {
						f.Plain = b
					}
				}
			}
		}
		out = append(out, f)
	}
	return out
}

// PostAppFixtures sends every fixture to the running server's real Sentry
// envelope endpoint and returns how many were accepted (HTTP 200). The
// fixtures' own DSN key is swapped for the test project's, so the sidecar
// query string is rewritten.
func PostAppFixtures(t *testing.T, env *Env, fixtures []AppFixture) int {
	t.Helper()
	if env.app == nil {
		t.Fatal("environment started without an app project")
	}
	accepted := 0
	for _, f := range fixtures {
		req, err := http.NewRequest(f.Method, env.URL()+"/api/42/envelope/?sentry_version=7&sentry_key="+appDSNKey, bytes.NewReader(f.Raw))
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range f.Headers {
			req.Header.Set(k, v)
		}
		res, err := env.Client().Do(req)
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		reply, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d %s", f.Name, res.StatusCode, reply)
			continue
		}
		accepted++
	}
	time.Sleep(300 * time.Millisecond) // the event writer flushes every 10 ms
	return accepted
}
