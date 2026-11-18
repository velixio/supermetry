package sentryingest

import (
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"supermetry/internal/obslogs"
	"supermetry/internal/obsquota"
)

type logSink struct {
	mu   sync.Mutex
	got  []obslogs.Record
	full bool
}

func (s *logSink) Enqueue(rs []obslogs.Record) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.full {
		return 0
	}
	s.got = append(s.got, rs...)
	return len(rs)
}

func (s *logSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

func jsLogEnvelope(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/logs/protocol/js.envelope")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func newLogEnv(t *testing.T, quota int64) (*env, *logSink, *obsquota.Tracker) {
	t.Helper()
	e := newEnv(t)
	sink := &logSink{}
	tr := obsquota.New(nil, func() time.Time { return *e.now })
	e.h.WithLogs(sink, tr)
	site := e.reg.sites[fdSite]
	site.App.LogDailyQuota = quota
	site.App.HashSecret = []byte("secret-secret-secret")
	e.reg.sites[fdSite] = site
	return e, sink, tr
}

func TestSentryLogItem(t *testing.T) {
	e, sink, _ := newLogEnv(t, 0)
	w := e.do(post(fdPath, fdQuery, jsLogEnvelope(t), nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if sink.count() != 3 {
		t.Fatalf("records = %d, want 3", sink.count())
	}
	wantSev := []int16{9, 13, 17}
	for i, r := range sink.got {
		if r.Source != obslogs.SourceSentry || r.Severity != wantSev[i] || r.Environment != "production" || r.Release != "web-2.4.0" {
			t.Errorf("record %d = %+v", i, r)
		}
		if hex.EncodeToString(r.TraceID) != "5b8efff798038103d269b633813fc60c" {
			t.Errorf("record %d trace id = %x", i, r.TraceID)
		}
	}
	first := sink.got[0]
	if first.Body != "checkout started" || first.TS.UnixMilli() != 1794923990250 {
		t.Errorf("first = %q at %v", first.Body, first.TS)
	}
	if len(first.Attributes) != 1 || first.Attributes["cart.items"] != int64(3) {
		t.Errorf("first attributes = %v (sentry.sdk.*, user.id and the environment must not appear)", first.Attributes)
	}
	if want := HashUser([]byte("secret-secret-secret"), "u-42"); string(first.UserHash) != string(want) || want == nil {
		t.Errorf("user hash = %x", first.UserHash)
	}
	second := sink.got[1]
	if second.Body != "slow response for [email]" {
		t.Errorf("body = %q", second.Body)
	}
	if len(second.Attributes) != 1 || second.Attributes["http.route"] != "/pay" {
		t.Errorf("second attributes = %v (authorization must be dropped)", second.Attributes)
	}
	third := sink.got[2]
	if third.Attributes["retry"] != true || third.Attributes["ratio"] != 0.5 {
		t.Errorf("third attributes = %v", third.Attributes)
	}
	// Log is no longer in the not-supported list.
	limits := w.Header().Get("X-Sentry-Rate-Limits")
	if limits == "" {
		t.Fatal("no rate limits header")
	}
	for _, cat := range strings.Split(strings.Split(limits, ":")[1], ";") {
		if cat == "log" {
			t.Errorf("log still in %q", limits)
		}
	}
}

func TestSentryLogItemsOffByDefault(t *testing.T) {
	e := newEnv(t)
	w := e.do(post(fdPath, fdQuery, jsLogEnvelope(t), nil))
	if w.Code != 200 || w.Header().Get("X-Sentry-Rate-Limits") != rejectUnsupported {
		t.Fatalf("%d limits=%q", w.Code, w.Header().Get("X-Sentry-Rate-Limits"))
	}
}

func TestSentryLogQuotaAndQueueFull(t *testing.T) {
	e, sink, tr := newLogEnv(t, 2)
	w := e.do(post(fdPath, fdQuery, jsLogEnvelope(t), nil))
	if w.Code != 200 || sink.count() != 2 || !strings.Contains(w.Header().Get("X-Sentry-Rate-Limits"), logLimits) {
		t.Fatalf("%d records=%d limits=%q", w.Code, sink.count(), w.Header().Get("X-Sentry-Rate-Limits"))
	}
	if tr.Used(fdSite, obsquota.ClassLogs) != 2 {
		t.Fatalf("used = %d", tr.Used(fdSite, obsquota.ClassLogs))
	}

	e2, sink2, tr2 := newLogEnv(t, 0)
	sink2.full = true
	if w := e2.do(post(fdPath, fdQuery, jsLogEnvelope(t), nil)); w.Code != 503 || w.Header().Get("Retry-After") != "5" {
		t.Fatalf("queue full: %d retry-after=%q", w.Code, w.Header().Get("Retry-After"))
	}
	if tr2.Used(fdSite, obsquota.ClassLogs) != 0 {
		t.Fatalf("a refused batch must not use quota, used = %d", tr2.Used(fdSite, obsquota.ClassLogs))
	}
}

func TestParseLogItemRejectsAndCaps(t *testing.T) {
	site := appSite(fdSite)
	site.App.HashSecret = []byte("k")
	old := fdNow.Add(-8 * 24 * time.Hour).Unix()
	future := fdNow.Add(11 * time.Minute).Unix()
	payload := `{"items":[` +
		`{"timestamp":` + strconv.FormatInt(old, 10) + `,"level":"info","body":"old"},` +
		`{"timestamp":` + strconv.FormatInt(future, 10) + `,"level":"info","body":"future"},` +
		`{"timestamp":` + strconv.FormatInt(fdNow.Unix(), 10) + `,"level":"fatal","body":"a\u0000b","attributes":{"k\u0000":{"value":"v\u0000w","type":"string"}}}]}`
	var st LogStats
	recs, err := ParseLogItem([]byte(payload), site, fdNow, &st)
	if err != nil || len(recs) != 1 || st.Rejected != 2 {
		t.Fatalf("recs=%d rejected=%d err=%v", len(recs), st.Rejected, err)
	}
	if recs[0].Severity != 21 || recs[0].Body != "ab" || recs[0].Attributes["k"] != "vw" {
		t.Fatalf("record = %+v", recs[0])
	}
	if _, err := ParseLogItem([]byte(`not json`), site, fdNow, nil); err == nil {
		t.Fatal("garbage accepted")
	}
}
