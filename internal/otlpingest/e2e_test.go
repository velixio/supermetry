package otlpingest

import (
	"context"
	"net/http"
	"testing"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/db"
	"supermetry/internal/sites"
	"supermetry/internal/spans"
	"supermetry/internal/testpg"
)

// TestEndToEndRowsInPostgres posts real requests and reads the rows back: the
// spans of the Go fixture land once (a re-post stores nothing new) and the
// exception of the Node fixture becomes one app_events row carrying the
// span's trace id.
func TestEndToEndRowsInPostgres(t *testing.T) {
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
	if err := db.EnsurePartitions(ctx, pool, testNow.AddDate(0, 0, -2), testNow.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	site := testSite()
	intake := appingest.NewIntake(appingest.Config{})
	appWriter := appingest.NewWriter(pool, intake, appingest.WriterConfig{FlushEvery: 10 * time.Millisecond})
	spanWriter := spans.NewWriter(pool, spans.WriterConfig{Flush: 10 * time.Millisecond, Now: func() time.Time { return testNow }}, &stats{}, nil)
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{}, 2)
	go func() { _ = appWriter.Run(wctx); done <- struct{}{} }()
	go func() { _ = spanWriter.Run(wctx); done <- struct{}{} }()
	t.Cleanup(func() { cancel(); <-done; <-done })

	h := NewHandler(registry{[]sites.Site{site}}, intake, spanWriter, nil, nil, func() time.Time { return testNow })
	r := &rig{h: h}
	for _, body := range [][]byte{marshalRequest(goHTTP()), marshalRequest(goHTTP()), marshalRequest(nodeExpress())} {
		if w := r.post(path42, body, nil); w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
	}
	var nSpans, nSegments int
	var nEvents int
	var traceMatch bool
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(ctx, `select count(*), count(*) filter (where is_segment) from spans where site_id=42`).Scan(&nSpans, &nSegments); err != nil {
			t.Log(err)
		}
		_ = pool.QueryRow(ctx, `select count(*), coalesce(bool_and(trace_id = $1), false) from app_events where site_id=42`, id16(9)).Scan(&nEvents, &traceMatch)
		if nSpans >= 5 && nEvents >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if nSpans != 5 || nSegments != 2 {
		t.Fatalf("spans=%d segments=%d, want 5 and 2 (duplicate body must store once)", nSpans, nSegments)
	}
	if nEvents != 1 || !traceMatch {
		t.Fatalf("app_events=%d traceMatch=%v", nEvents, traceMatch)
	}
	var desc string
	if err := pool.QueryRow(ctx, `select description from spans where site_id=42 and op='db'`).Scan(&desc); err != nil || desc != "select * from users where email = ? and id = ?" {
		t.Fatalf("stored description %q (%v)", desc, err)
	}
}
