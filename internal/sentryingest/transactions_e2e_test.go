package sentryingest

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/appquery"
	"supermetry/internal/db"
	"supermetry/internal/spanrollup"
	"supermetry/internal/spans"
	"supermetry/internal/testpg"
)

// txE2E is one transaction fixture through the real handler, the span writer
// (CopyFrom into Postgres), the hourly rollup and the read queries.
type txE2E struct {
	file    string
	trace   string
	name    string // the segment (transaction) name the list must show
	spans   int    // rows in the trace
	appType string // app start type, "" for none
	appMS   float64
	// tree is the waterfall in display order: "depth op description".
	tree []string
}

const e2eTrace = "5f1c0a9e7b3d4c2a8e6f1d0b9a8c7e6d"

var txE2ECases = []txE2E{
	{file: "python", trace: "", name: "GET /api/items", spans: 3, tree: []string{
		"0 http.server",
		"1 db.query select * from items where owner = ? and id = ?",
		"1 http.client GET https://api.example.com/v1/items/*",
	}},
	{file: "javascript-static", trace: e2eTrace, name: "/users/*", spans: 4, tree: []string{
		"0 pageload",
		"1 browser.request GET /users/1234",
		"1 resource.script https://app.example.com/assets/index-3f2a9c.js",
		"1 http.client GET https://api.example.com/v1/me",
	}},
	{file: "javascript-v11-stream", trace: e2eTrace, name: "/users/1234", spans: 3, tree: []string{
		"0 pageload",
		"1 http.client GET https://api.example.com/v1/me",
		"1 db.query select * from settings where owner = ?",
	}},
	{file: "node-v11-stream", trace: "11301c7429e3411eac9af87bc9dabc42", name: "GET /users/:id", spans: 3, tree: []string{
		"0 http.server",
		"1 db.query select * from users where email = ? and id = ?",
		"1 http.client GET https://api.example.com/v1/items/*",
	}},
	{file: "go-v0.50", trace: "", name: "GET /api/items", spans: 3, tree: []string{
		"0 http.server",
		"1 db.query select * from items where owner = ? and id = ?",
		"1 http.client GET https://api.example.com/v1/items/*",
	}},
	{file: "browser-v11-pageload", trace: "27032a2496834aea96a174202641d54c", name: "/", spans: 13, tree: []string{
		"0 pageload",
		"1 http.client GET 127.0.0.1",
		"1 browser.dom_content_loaded_event DOMContentLoaded event",
		"1 browser.load_event Load event",
		"1 browser.connect Connect",
		"1 browser.cache Cache lookup",
		"1 browser.dns DNS lookup",
		"1 browser.request Request",
		"1 browser.response Response",
		"1 resource.script [ip]",
		"1 browser.paint first-paint",
		"1 browser.paint first-contentful-paint",
		"1 ui.webvital.cls Layout shift",
	}},
	{file: "android-app-start", trace: e2eTrace, name: "MainActivity", spans: 4, appType: "cold", appMS: 1234, tree: []string{
		"0 ui.load",
		"1 process.load Process initialization",
		"1 application.load ExampleApplication.onCreate",
		"1 activity.load MainActivity.onCreate",
	}},
	{file: "cocoa-app-start", trace: e2eTrace, name: "ViewController", spans: 4, appType: "warm", appMS: 620, tree: []string{
		"0 ui.load",
		"1 app.start.cold Pre Runtime Init",
		"1 app.start.cold UIKit Init",
		"1 app.start.cold Initial Frame Render",
	}},
}

func TestTransactionFixturesEndToEnd(t *testing.T) {
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
	if err := db.EnsureDailyPartitions(ctx, pool, "spans", time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	q := appquery.New(pool, func() time.Time { return fdNow })
	scope := appquery.Scope{SiteID: fdSite, OrgID: 1, TZ: time.UTC, RetentionDays: 30}

	for _, c := range txE2ECases {
		t.Run(c.file, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `truncate spans, span_hourly`); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile("testdata/transactions/" + c.file + ".envelope")
			if err != nil {
				t.Fatal(err)
			}
			w := spans.NewWriter(pool, spans.WriterConfig{Flush: time.Hour, Now: func() time.Time { return fdNow },
				Location: func(int32) *time.Location { return time.UTC }}, nil, nil)
			wctx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- w.Run(wctx) }()

			e := newEnv(t)
			e.h.WithSpans(w, spans.NewBudget(60000))
			// The same envelope twice: a retried transaction is stored once
			// (span containers have no event id, so those are sent once).
			sends := 1
			if !strings.Contains(c.file, "v11") {
				sends = 2
			}
			for i := 0; i < sends; i++ {
				var hdr map[string]string
				if len(raw) > 2 && raw[0] == 0x1f && raw[1] == 0x8b {
					hdr = map[string]string{"Content-Encoding": "gzip"}
				}
				if rec := e.do(post(fdPath, fdQuery, string(raw), hdr)); rec.Code != http.StatusOK {
					t.Fatalf("status %d: %s", rec.Code, rec.Body)
				}
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if err := spanrollup.RecomputeHour(ctx, pool, fdSite, time.UTC, time.Date(2026, 11, 17, 13, 0, 0, 0, time.UTC)); err != nil {
				t.Fatal(err)
			}

			rows, err := q.Transactions(ctx, scope, appquery.Filter{From: day, To: day}, "count", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].Name != c.name || rows[0].Count != 1 || rows[0].Errors != 0 {
				t.Fatalf("transactions = %+v, want one %q with count 1", rows, c.name)
			}
			// One 1.5 s transaction (python: recorded, 0.9 ms): the percentiles sit in its bucket.
			wantMS := 1500.0
			if c.file == "python" {
				wantMS = 0.9 // the recorded transaction took under a millisecond
			}
			switch c.file {
			case "node-v11-stream":
				wantMS = 31.1 // recorded duration, 1794923880.0 .. +0.0311 s
			case "browser-v11-pageload":
				wantMS = 622.3 // recorded pageload segment
			case "go-v0.50":
				wantMS = 25.4 // recorded: 08:26:10.477188 to .502546
			}
			if math.Abs(rows[0].P50-wantMS)/wantMS > 0.0488+0.002 {
				t.Errorf("p50 = %v ms, want %v within the histogram error", rows[0].P50, wantMS)
			}

			trace := c.trace
			if trace == "" {
				if err := pool.QueryRow(ctx, `select encode(trace_id,'hex') from spans limit 1`).Scan(&trace); err != nil {
					t.Fatal(err)
				}
			}
			var id [16]byte
			if _, err := hex.Decode(id[:], []byte(trace)); err != nil {
				t.Fatal(err)
			}
			tr, err := q.Trace(ctx, scope, id)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, s := range tr.Spans {
				line := fmt.Sprintf("%d %s", s.Depth, s.Op)
				if s.Description != "" && s.Depth > 0 {
					line += " " + s.Description
				}
				got = append(got, line)
			}
			if len(tr.Spans) != c.spans || !sameSet(got, c.tree) {
				t.Errorf("trace = %q, want %q", got, c.tree)
			}
			if got[0] != c.tree[0] {
				t.Errorf("trace root = %q, want %q", got[0], c.tree[0])
			}
			for _, s := range tr.Spans[1:] {
				if s.ParentID != tr.Spans[0].SpanID {
					t.Errorf("span %s parent %s, want the segment %s", s.SpanID, s.ParentID, tr.Spans[0].SpanID)
				}
			}

			if c.appType != "" {
				d, err := q.Transaction(ctx, scope, appquery.Filter{From: day, To: day}, c.name)
				if err != nil {
					t.Fatal(err)
				}
				if len(d.AppStart) != 1 || d.AppStart[0].Type != c.appType || d.AppStart[0].Count != 1 ||
					math.Abs(d.AppStart[0].P50-c.appMS)/c.appMS > 0.0488 {
					t.Errorf("app start = %+v, want one %s start of %v ms within 4.88%%", d.AppStart, c.appType, c.appMS)
				}
			}
			privacyScan(t, pool)
		})
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
	}
	for _, n := range m {
		if n != 0 {
			return false
		}
	}
	return true
}

// privacyScan dumps every stored span and rollup row as text and fails on a
// fixture literal that must not survive: emails, tokens, query strings, the
// sender's address and cookie values.
func privacyScan(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var dump string
	if err := pool.QueryRow(context.Background(), `select coalesce((select string_agg(s::text, E'\n') from spans s), '')
|| coalesce((select string_agg(h::text, E'\n') from span_hourly h), '')`).Scan(&dump); err != nil {
		t.Fatal(err)
	}
	if len(dump) == 0 {
		t.Fatal("nothing stored to scan")
	}
	for _, bad := range []string{"user@example.com", "access_token", "fixture-token", "token=", "owner = 'fixture'", "'fixture'", "192.0.2.", "cookie", "sekret"} {
		if strings.Contains(strings.ToLower(dump), strings.ToLower(bad)) {
			t.Errorf("stored spans contain %q", bad)
		}
	}
}
