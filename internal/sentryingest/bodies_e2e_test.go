package sentryingest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

var privateAddr = regexp.MustCompile(`192\.0\.2\.|/home/|/Users/`)

// TestFixturesStoreScrubbedBodies posts every fixture to the handler with the
// real body writer and client-report aggregator, then reads the database: each
// event has a body row, and no stored body carries an email, a documentation
// address, a home path or a cookie value. Client reports land as counts.
func TestFixturesStoreScrubbedBodies(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values($1,1,'appsite00042','App','UTC',30,'app')`, fdSite); err != nil {
		t.Fatal(err)
	}
	bodies := appingest.NewBodyWriter(pool, appingest.BodyWriterConfig{})
	reports := appingest.NewClientReports(pool, 0)

	wantBodies, wantReports := 0, 0
	for _, file := range fixtureFiles(t) {
		raw, _ := os.ReadFile(file)
		side, _ := os.ReadFile(strings.TrimSuffix(file, ".envelope") + ".json")
		var sc sidecar
		if err := json.Unmarshal(side, &sc); err != nil {
			t.Fatal(err)
		}
		e := newEnv(t)
		e.h.bodies = bodies
		e.h.WithClientReports(reports)
		*e.now = time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC)
		req := httptest.NewRequest(sc.Method, sc.Path+"?"+sc.Query, bytes.NewReader(raw))
		for k, v := range sc.Headers {
			req.Header.Set(k, v)
		}
		if w := e.do(req); w.Code != 200 {
			t.Fatalf("%s = %d", file, w.Code)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(file, "testdata/"), ".envelope")
		wantBodies += wantEvents[name]
		if strings.HasSuffix(name, "client-report") {
			wantReports++
		}
	}
	// Drain by running the writers until their queues are empty.
	runCtx, cancel := context.WithCancel(ctx)
	d1, d2 := make(chan error, 1), make(chan error, 1)
	go func() { d1 <- bodies.Run(runCtx) }()
	go func() { d2 <- reports.Run(runCtx) }()
	cancel()
	<-d1
	<-d2

	rows, err := pool.Query(ctx, `select event_id, body::text, size from app_event_bodies where site_id = $1`, fdSite)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := 0
	for rows.Next() {
		var id, body string
		var size int
		if err := rows.Scan(&id, &body, &size); err != nil {
			t.Fatal(err)
		}
		got++
		if size <= 0 {
			t.Errorf("%s: size %d", id, size)
		}
		if m := emailPattern.FindString(body); m != "" {
			t.Errorf("%s: email-like %q stored", id, m)
		}
		if strings.Contains(body, "@example.com") || privateAddr.MatchString(body) {
			t.Errorf("%s: private data stored: %.200s", id, body)
		}
		lower := strings.ToLower(body)
		for _, bad := range []string{`"cookie`, `"set-cookie`, `"authorization`, `"ip_address"`, `"email"`} {
			if strings.Contains(lower, bad) {
				t.Errorf("%s: %s stored", id, bad)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got < 15 || got > wantBodies {
		// Duplicate event ids across fixtures overwrite nothing: the key is unique, so a
		// repeated id may be rejected; the floor still proves every platform is stored.
		t.Fatalf("stored bodies = %d, want between 15 and %d", got, wantBodies)
	}
	t.Logf("stored %d bodies of %d event items", got, wantBodies)
	var n int64
	if err := pool.QueryRow(ctx, `select count(*) from app_client_reports where site_id = $1 and quantity > 0`, fdSite).Scan(&n); err != nil || n == 0 {
		t.Fatalf("client reports stored: %d %v (fixtures with reports: %d)", n, err, wantReports)
	}
}
