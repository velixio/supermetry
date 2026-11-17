// Package ingesttest wires the Sentry front door to the real writer and a
// disposable database, so tests in other packages can ingest an envelope and
// read the result through the existing alert and query code.
package ingesttest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/appingest"
	"supermetry/internal/db"
	"supermetry/internal/sentryingest"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

const (
	SiteID = int32(42)
	Key    = "0123456789abcdef0123456789abcdef"
)

// Env is one app site behind a running writer.
type Env struct {
	Pool    *pgxpool.Pool
	Site    sites.Site
	Now     time.Time
	handler *sentryingest.Handler
}

type registry struct{ site sites.Site }

func (r registry) ByPublicID(string) (sites.Site, bool) { return sites.Site{}, false }
func (r registry) Site(id int32) (sites.Site, bool)     { return r.site, id == r.site.ID }
func (r registry) All() []sites.Site                    { return []sites.Site{r.site} }
func (r registry) ByDSNKey(k string) (sites.Site, sites.DSNKey, bool) {
	for _, dk := range r.site.App.DSNKeys {
		if dk.PublicKey == k {
			return r.site, dk, true
		}
	}
	return sites.Site{}, sites.DSNKey{}, false
}

// New migrates a fresh database, creates app site 42 with the given tag
// allowlist and starts the writer; it stops with the test.
func New(t *testing.T, now time.Time, tagAllowlist ...string) *Env {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(42,1,'appsite00042','App','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	site := sites.Site{ID: SiteID, OrgID: 1, Kind: sites.KindApp, TZ: time.UTC, App: &sites.AppConfig{
		DSNKeys: []sites.DSNKey{{PublicKey: Key}}, MaxReportAgeDays: 7, TagAllowlist: tagAllowlist, HashSecret: []byte("0123456789abcdef0123456789abcdef"),
	}}
	intake := appingest.NewIntake(appingest.Config{})
	writer := appingest.NewWriter(pool, intake, appingest.WriterConfig{FlushEvery: 10 * time.Millisecond})
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = writer.Run(wctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	h := sentryingest.NewHandler(registry{site}, intake, nil, nil, nil, func() time.Time { return now }).WithIPLimit(-1)
	return &Env{Pool: pool, Site: site, Now: now, handler: h}
}

// Post sends an envelope to the front door and returns the HTTP status; it
// then waits until the writer has stored everything that was accepted.
func (e *Env) Post(t *testing.T, envelope string) int {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/42/envelope/?sentry_version=7&sentry_key="+Key, strings.NewReader(envelope))
	w := httptest.NewRecorder()
	e.handler.ServeHTTP(w, r)
	time.Sleep(200 * time.Millisecond) // writer flushes every 10 ms
	return w.Code
}

// Envelope reads a recorded fixture (such as "node/unhandled-exception") and
// returns it with the event item's JSON passed through mutate (nil keeps it).
// Only single-event fixtures with uncompressed bodies are supported.
func Envelope(t *testing.T, name string, mutate func(event map[string]any)) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "testdata", name+".envelope"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) < 3 || mutate == nil {
		return string(raw)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &event); err != nil {
		t.Fatal(err)
	}
	mutate(event)
	out, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	lines[2] = string(out)
	return strings.Join(lines, "\n")
}
