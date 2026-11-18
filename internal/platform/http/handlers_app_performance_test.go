package platformhttp_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/spans"
)

// perfFixture is an app project with transactions and one trace.
type perfFixture struct {
	healthFixture
	traceHex string
}

func newPerfFixture(t *testing.T) perfFixture {
	t.Helper()
	h := newAppHealthFixture(t)
	ctx := context.Background()
	if err := db.EnsureDailyPartitions(ctx, h.pool, "spans", time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	insert := func(i int, trace [16]byte, span, parent []byte, segment bool, name, op, desc string, durUS int64) {
		var parentArg any
		if parent != nil {
			parentArg = parent
		}
		if _, err := h.pool.Exec(ctx, `insert into spans(site_id,start_ts,duration_us,bucket,trace_id,span_id,parent_span_id,segment_id,is_segment,name,op,description,release,environment,platform)
values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'1.0.0','production','linux')`,
			h.siteID, start.Add(time.Duration(i)*time.Second), durUS, spans.Bucket(durUS), trace[:], span, parentArg, span, segment, name, op, desc); err != nil {
			t.Fatal(err)
		}
	}
	var trace [16]byte
	trace[15] = 1
	root := []byte{0, 0, 0, 0, 0, 0, 0, 1}
	insert(0, trace, root, nil, true, "GET /items", "http.server", "", 120_000)
	for i := 2; i <= 20; i++ {
		var tr [16]byte
		tr[15] = byte(i)
		insert(i, tr, []byte{0, 0, 0, 0, 0, 0, 0, byte(i)}, nil, true, "GET /items", "http.server", "", int64(i)*10_000)
	}
	// A child of the trace, on its own segment id.
	if _, err := h.pool.Exec(ctx, `insert into spans(site_id,start_ts,duration_us,bucket,trace_id,span_id,parent_span_id,segment_id,is_segment,name,op,description)
values($1,$2,40000,$3,$4,$5,$6,$6,false,'','db','select * from items where id = ?')`,
		h.siteID, start.Add(10*time.Millisecond), spans.Bucket(40_000), trace[:], []byte{0, 0, 0, 0, 0, 0, 0, 9}, root); err != nil {
		t.Fatal(err)
	}
	return perfFixture{healthFixture: h, traceHex: hex.EncodeToString(trace[:])}
}

const perfRange = "from=2026-10-12&to=2026-10-12"

func TestAppPerformanceReads(t *testing.T) {
	h := newPerfFixture(t)
	for _, actor := range []string{"owner", "editor", "viewer"} {
		for suffix, key := range map[string]string{
			"performance?" + perfRange:                                          "transactions",
			"transactions?" + perfRange + "&sort=count":                         "transactions",
			"transactions/detail?" + perfRange + "&name=GET%20%2Fitems":         "span_groups",
			"traces/" + h.traceHex:                                              "spans",
			"performance/regressions?" + perfRange + "&base=1.0.0&target=2.0.0": "regressions",
			"performance/network?" + perfRange:                                  "failures",
		} {
			code, body, raw := h.get(t, actor, suffix)
			if code != http.StatusOK || body[key] == nil {
				t.Fatalf("%s %s = %d %s", actor, suffix, code, raw)
			}
		}
	}
	code, body, raw := h.get(t, "owner", "transactions?"+perfRange)
	rows := body["transactions"].([]any)
	if code != http.StatusOK || len(rows) != 1 || rows[0].(map[string]any)["count"] != float64(20) {
		t.Fatalf("transactions = %d %s", code, raw)
	}
	code, body, raw = h.get(t, "owner", "traces/"+h.traceHex)
	if spansOut := body["spans"].([]any); code != http.StatusOK || len(spansOut) != 2 || spansOut[1].(map[string]any)["depth"] != float64(1) {
		t.Fatalf("trace = %d %s", code, raw)
	}
}

func TestAppPerformanceValidation(t *testing.T) {
	h := newPerfFixture(t)
	for suffix, want := range map[string]int{
		"traces/not-hex":                                     http.StatusBadRequest,
		"traces/" + strings.Repeat("a", 300):                 http.StatusBadRequest,
		"traces/" + strings.Repeat("0", 32):                  http.StatusNotFound,
		"transactions?" + perfRange + "&sort=x":              http.StatusBadRequest,
		"transactions?" + perfRange + "&limit=0":             http.StatusBadRequest,
		"transactions/detail?" + perfRange:                   http.StatusBadRequest,
		"transactions/detail?" + perfRange + "&name=missing": http.StatusNotFound,
		"performance/regressions?" + perfRange:               http.StatusBadRequest,
	} {
		if code, _, raw := h.get(t, "owner", suffix); code != want {
			t.Errorf("%s = %d, want %d: %s", suffix, code, want, raw)
		}
	}
}

func TestAppPerformanceAuthorizationAndTenantBoundaries(t *testing.T) {
	h := newPerfFixture(t)
	routes := []string{
		"performance?" + perfRange,
		"transactions?" + perfRange,
		"transactions/detail?" + perfRange + "&name=GET%20%2Fitems",
		"traces/" + h.traceHex,
		"performance/regressions?" + perfRange + "&base=1.0.0&target=2.0.0",
		"performance/network?" + perfRange,
	}
	for _, suffix := range routes {
		// Another organisation and an actor restricted to other sites: 404.
		for _, actor := range []string{"foreign-owner", "other-editor"} {
			if code, _, raw := h.get(t, actor, suffix); code != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 404: %s", actor, suffix, code, raw)
			}
		}
		if res := h.request(t, "", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/app/%s", h.siteID, suffix), ``); res.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s = %d", suffix, res.Code)
		}
		// A site of the same organisation that is not an app project.
		path := fmt.Sprintf("/api/private/sites/%d/app/%s", h.otherSite, suffix)
		if res := h.request(t, "owner", http.MethodGet, path, ``); res.Code != http.StatusNotFound {
			t.Errorf("web site %s = %d, want 404", suffix, res.Code)
		}
	}
	// The trace of this site is not reachable through another app's path.
	if _, err := h.pool.Exec(context.Background(), `update sites set kind='app' where id=$1`, h.otherSite); err != nil {
		t.Fatal(err)
	}
	if err := h.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	if code, _, raw := h.getSite(t, "owner", h.otherSite, "traces/"+h.traceHex); code != http.StatusNotFound {
		t.Errorf("trace through another app = %d, want 404: %s", code, raw)
	}
	if code, body, raw := h.getSite(t, "owner", h.otherSite, "transactions?"+perfRange); code != http.StatusOK || len(body["transactions"].([]any)) != 0 {
		t.Errorf("other app transactions = %d %s", code, raw)
	}
	// Web projects have no performance reads.
	if _, err := h.pool.Exec(context.Background(), `update sites set kind='web' where id=$1`, h.siteID); err != nil {
		t.Fatal(err)
	}
	if err := h.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := h.get(t, "owner", "performance?"+perfRange); code != http.StatusNotFound {
		t.Errorf("web project = %d", code)
	}
}

func (h healthFixture) getSite(t *testing.T, actor string, site int32, suffix string) (int, map[string]any, string) {
	t.Helper()
	res := h.request(t, actor, http.MethodGet, fmt.Sprintf("/api/private/sites/%d/app/%s", site, suffix), ``)
	body := map[string]any{}
	_ = json.Unmarshal(res.Body.Bytes(), &body)
	return res.Code, body, res.Body.String()
}
