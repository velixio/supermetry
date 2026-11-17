package appingest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/appproduct"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/privacy"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
	"supermetry/internal/writer"
)

// productEnv is a handler wired to the real product adapter, web writer and
// app writer over a test database.
type productEnv struct {
	*handlerEnv
	pool *pgxpool.Pool
	stop func()
}

type productClock struct{ now time.Time }

func (c productClock) Now() time.Time { return c.now }

func newProductEnv(t *testing.T) *productEnv {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, testNow.AddDate(0, 0, -10), testNow.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(7,1,'appsite00001','App','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	env := newHandlerEnv(t, Config{}, func(s *sites.Site) {
		s.App.HashSecret = []byte("0123456789abcdef0123456789abcdef")
		s.ProfilesEnabled = true
	})
	clock := productClock{now: testNow}
	sess := session.NewMemory(1, clock)
	w := writer.New(pool, sess, 100, time.Hour, time.Hour)
	w.SetClock(clock)
	w.SetTrackingPolicy(privacy.Identified, fakeRegistry{byPublic: map[string]sites.Site{env.site.PublicID: env.site}})
	adapter := appproduct.New(w, sess, dict.New(pool), nil, func() time.Time { return testNow })
	env.h.WithProduct(adapter)
	appWriter := NewWriter(pool, env.in, WriterConfig{FlushEvery: 10 * time.Millisecond})

	runCtx, cancel := context.WithCancel(ctx)
	var done []chan error
	for _, run := range []func(context.Context) error{w.Run, appWriter.Run} {
		ch := make(chan error, 1)
		done = append(done, ch)
		go func() { ch <- run(runCtx) }()
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			for _, ch := range done {
				if err := <-ch; err != nil {
					t.Errorf("writer: %v", err)
				}
			}
		})
	}
	t.Cleanup(stop)
	return &productEnv{handlerEnv: env, pool: pool, stop: stop}
}

func productJSON(id, kind, extra string) string {
	return fmt.Sprintf(`{"id":%q,"kind":%q,"ts":%d,"install_id":"install-raw-123","platform":"ios","release":"1.0.0"%s}`, id, kind, testNow.UnixMilli(), extra)
}

func scalar(t *testing.T, pool *pgxpool.Pool, query string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func decodeEvent(t *testing.T, raw string) map[string]any {
	t.Helper()
	obj, err := decodeObject([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return obj
}

func TestHandlerProductKindsRouteToAdapter(t *testing.T) {
	env := newProductEnv(t)
	rr := env.post(batch(env.site.PublicID,
		productJSON("e1", "error", `,"error_type":"Boom","frames":"a.go:1 f"`),
		productJSON("s1", "screen", `,"screen":"home"`),
		productJSON("p1", "event", `,"name":"purchase","props":{"plan":"pro"}`),
	), env.key, "203.0.113.1")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	var got reply
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || got.Accepted != 3 || len(got.Dropped) != 0 {
		t.Fatalf("reply %+v err=%v", got, err)
	}
	env.stop()
	if n := scalar(t, env.pool, `select count(*) from app_events`); n != 1 {
		t.Fatalf("app_events rows=%d, want 1 (the error only)", n)
	}
	if n := scalar(t, env.pool, `select count(*) from events where site_id=7`); n != 2 {
		t.Fatalf("events rows=%d, want 2 (screen and event)", n)
	}
	if n := scalar(t, env.pool, `select count(*) from events where site_id=7 and kind=0 and props->>'$release'='1.0.0'`); n != 1 {
		t.Fatalf("screen row missing: %d", n)
	}
	if n := scalar(t, env.pool, `select count(*) from events where site_id=7 and kind=1 and props->>'plan'='pro'`); n != 1 {
		t.Fatalf("event row missing: %d", n)
	}
}

func TestHandlerProductDuplicateAndNoSink(t *testing.T) {
	env := newProductEnv(t)
	body := batch(env.site.PublicID, productJSON("dup", "screen", `,"screen":"home"`), productJSON("dup", "screen", `,"screen":"home"`))
	var got reply
	if err := json.Unmarshal(env.post(body, env.key, "203.0.113.1").Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Accepted != 1 || len(got.Dropped) != 1 || got.Dropped[0].Reason != ReasonDuplicate {
		t.Fatalf("reply %+v", got)
	}
	bare := newHandlerEnv(t, Config{}, nil)
	got = reply{}
	if err := json.Unmarshal(bare.post(batch(bare.site.PublicID, productJSON("x", "screen", `,"screen":"home"`)), bare.key, "203.0.113.2").Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Accepted != 0 || len(got.Dropped) != 1 || got.Dropped[0].Reason != ReasonInvalid {
		t.Fatalf("no sink: %+v", got)
	}
}

func TestHandlerProductKindValidation(t *testing.T) {
	env := newHandlerEnv(t, Config{}, nil)
	for name, event := range map[string]string{
		"bad name":        productJSON("a", "event", `,"name":"bad/name"`),
		"dollar name":     productJSON("b", "event", `,"name":"$internal"`),
		"missing name":    productJSON("c", "event", ``),
		"missing screen":  productJSON("d", "screen", ``),
		"long screen":     productJSON("e", "screen", `,"screen":"`+strings.Repeat("s", 201)+`"`),
		"dollar prop key": productJSON("f", "event", `,"name":"ok","props":{"$release":"x"}`),
		"non-string prop": productJSON("g", "event", `,"name":"ok","props":{"n":1}`),
		"no install":      fmt.Sprintf(`{"id":"h","kind":"screen","screen":"home","ts":%d}`, testNow.UnixMilli()),
	} {
		if _, err := ParseEvent(decodeEvent(t, event)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := `{"id":"ok","kind":"event","name":"Level 2: done-1.x","install_id":"i","props":{"k":"v"}}`
	if e, err := ParseEvent(decodeEvent(t, ok)); err != nil || !e.IsProduct() || e.Name != "Level 2: done-1.x" || e.Props["k"] != "v" {
		t.Fatalf("valid event: %+v err=%v", e, err)
	}
	rr := env.post(batch(env.site.PublicID, productJSON("z", "event", `,"name":"bad/name"`)), env.key, "203.0.113.3")
	var got reply
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || len(got.Dropped) != 1 || got.Dropped[0].Reason != ReasonInvalid {
		t.Fatalf("reply %+v err=%v", got, err)
	}
}

func TestHandlerProductNoRawInstall(t *testing.T) {
	env := newProductEnv(t)
	rr := env.post(batch(env.site.PublicID,
		productJSON("e1", "error", `,"session_id":"session-raw-456","error_type":"Boom","frames":"a.go:1 f"`),
		productJSON("s1", "screen", `,"screen":"home","session_id":"session-raw-456"`),
		productJSON("p1", "event", `,"name":"purchase"`),
	), env.key, "203.0.113.1")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	env.stop()
	rows, err := env.pool.Query(context.Background(), `select table_name from information_schema.tables
		where table_schema = current_schema() and table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		query := fmt.Sprintf(`select count(*) from %s t where t::text like '%%install-raw-123%%' or t::text like '%%session-raw-456%%'`, pgx.Identifier{table}.Sanitize())
		if leaked := scalar(t, env.pool, query); leaked != 0 {
			t.Errorf("raw id leaked into %s (%d rows)", table, leaked)
		}
	}
	if scalar(t, env.pool, `select count(*) from events where site_id=7`) != 2 {
		t.Fatal("product events were not stored")
	}
}
