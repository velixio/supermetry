package sentryingest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"supermetry/internal/sites"
	"supermetry/internal/spans"
)

var txNow = time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC)

const (
	txTrace = "0af7651916cd43dd8448eb211c80319c"
	txRoot  = "b9c7c989f97918e1"
)

func txSite() sites.Site {
	return sites.Site{ID: 42, Kind: sites.KindApp, TZ: time.UTC, App: &sites.AppConfig{MaxReportAgeDays: 7}}
}

func txStart() float64 { return float64(txNow.Add(-time.Minute).UnixMilli()) / 1000 }

func txPayload(extra string, spanJSON ...string) []byte {
	s := txStart()
	return []byte(fmt.Sprintf(`{"type":"transaction","event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","transaction":"GET /api/items","transaction_info":{"source":"route"},
"start_timestamp":%.3f,"timestamp":%.3f,"release":"app@1.0","environment":"prod","platform":"python",
"contexts":{"trace":{"trace_id":"%s","span_id":"%s","op":"http.server","status":"ok","data":{"http.response.status_code":200}}},
"spans":[%s]%s}`, s, s+0.25, txTrace, txRoot, strings.Join(spanJSON, ","), extra))
}

func txSpan(id, parent, op, desc string, off, dur float64) string {
	s := txStart() + off
	return fmt.Sprintf(`{"span_id":"%s","parent_span_id":"%s","trace_id":"%s","op":"%s","description":%q,"start_timestamp":%.3f,"timestamp":%.3f,"status":"ok"}`, id, parent, txTrace, op, desc, s, s+dur)
}

func parseTx(t *testing.T, raw []byte) ([]spans.Span, *SpanStats) {
	t.Helper()
	var st SpanStats
	out, err := parseTransaction(raw, EventContext{}, txSite(), txNow, &st)
	if err != nil {
		t.Fatal(err)
	}
	return out, &st
}

func TestParseTransactionTree(t *testing.T) {
	out, _ := parseTx(t, txPayload("",
		txSpan("1111111111111111", txRoot, "db.query", "select * from users where email = 'a@b.c' and id = 42", 0.01, 0.05),
		txSpan("2222222222222222", "1111111111111111", "http.client", "GET https://api.example.com/v1/items/12345?token=sk_live_secret", 0.07, 0.1)))
	if len(out) != 3 {
		t.Fatalf("got %d spans", len(out))
	}
	seg := out[0]
	if !seg.IsSegment || seg.Name != "GET /api/items" || seg.Op != "http.server" || seg.Release != "app@1.0" || seg.Environment != "prod" || seg.Platform != "python" ||
		seg.DurationUS != 250_000 || seg.SiteID != 42 || seg.ParentID != nil || seg.Status != "ok" || seg.HTTPStatus == nil || *seg.HTTPStatus != 200 {
		t.Fatalf("segment %+v", seg)
	}
	if seg.SegmentID != seg.SpanID || fmt.Sprintf("%x", seg.TraceID) != txTrace || fmt.Sprintf("%x", seg.SpanID) != txRoot {
		t.Fatalf("segment ids %+v", seg)
	}
	db, http := out[1], out[2]
	if db.Description != "select * from users where email = ? and id = ?" || db.Op != "db.query" || db.IsSegment || db.SegmentID != seg.SpanID {
		t.Errorf("db span %+v", db)
	}
	if db.ParentID == nil || fmt.Sprintf("%x", *db.ParentID) != txRoot {
		t.Errorf("db parent %v", db.ParentID)
	}
	if http.Description != "GET https://api.example.com/v1/items/*" || http.ParentID == nil || fmt.Sprintf("%x", *http.ParentID) != "1111111111111111" {
		t.Errorf("http span %+v", http)
	}
	if db.DurationUS != 50_000 {
		t.Errorf("duration/hash %d", db.DurationUS)
	}
	for _, s := range out {
		if strings.Contains(s.Description+s.Name, "a@b.c") || strings.Contains(s.Description, "sk_live") {
			t.Errorf("leak in %+v", s)
		}
	}
}

func TestParseTransactionNoChildrenStoresSegment(t *testing.T) {
	out, _ := parseTx(t, txPayload(""))
	if len(out) != 1 || !out[0].IsSegment {
		t.Fatalf("%+v", out)
	}
}

func TestParseTransactionNameURLSource(t *testing.T) {
	raw := strings.Replace(string(txPayload("")), `"transaction":"GET /api/items","transaction_info":{"source":"route"}`,
		`"transaction":"/users/123/orders/9f8e7d6c5b4a3210","transaction_info":{"source":"url"}`, 1)
	out, _ := parseTx(t, []byte(raw))
	if out[0].Name != "/users/*/orders/*" {
		t.Errorf("name %q", out[0].Name)
	}
}

func TestParseTransactionAppStart(t *testing.T) {
	for _, c := range []struct {
		measurements, typ string
		ms                int32
	}{
		{`,"measurements":{"app_start_cold":{"value":1234.5,"unit":"millisecond"}}`, "cold", 1234},
		{`,"measurements":{"app_start_warm":{"value":0.8,"unit":"second"}}`, "warm", 800},
		{`,"measurements":{"app_start_cold":{"value":1234}}`, "cold", 1234},
	} {
		out, _ := parseTx(t, txPayload(c.measurements))
		s := out[0]
		if s.AppStartType != c.typ || s.AppStartMS == nil || *s.AppStartMS != c.ms || s.AppStartBucket == nil || *s.AppStartBucket != spans.Bucket(int64(c.ms)*1000) {
			t.Errorf("%s: %+v", c.measurements, s)
		}
	}
	// Absurd or negative values are ignored.
	for _, m := range []string{`,"measurements":{"app_start_cold":{"value":-5}}`, `,"measurements":{"app_start_cold":{"value":9e12}}`, `,"measurements":{"app_start_cold":"x"}`} {
		out, _ := parseTx(t, txPayload(m))
		if out[0].AppStartType != "" || out[0].AppStartMS != nil {
			t.Errorf("%s: %+v", m, out[0])
		}
	}
	out, _ := parseTx(t, txPayload(`,"measurements":{"app_start_cold":{"value":900}}`, txSpan("1111111111111111", txRoot, "x", "d", 0, 0.1)))
	if out[1].AppStartType != "" {
		t.Error("app start belongs on the segment only")
	}
}

func TestParseTransactionClampsAndLimits(t *testing.T) {
	// End before start: duration 0, counted.
	s := txStart()
	bad := fmt.Sprintf(`{"span_id":"1111111111111111","parent_span_id":"%s","op":"x","description":"d","start_timestamp":%.3f,"timestamp":%.3f}`, txRoot, s+1, s)
	out, st := parseTx(t, txPayload("", bad))
	if out[1].DurationUS != 0 || st.Clamped != 1 {
		t.Errorf("clamp: %+v %+v", out[1], st)
	}
	// 1,001 child spans: the segment plus 1,000 stored, the rest counted.
	var many []string
	for i := range 1001 {
		many = append(many, txSpan(fmt.Sprintf("%016x", i+1), txRoot, "x", "d", 0, 0.01))
	}
	out, st = parseTx(t, txPayload("", many...))
	if len(out) != 1001 || st.Truncated != 1 {
		t.Errorf("truncate: len %d, truncated %d", len(out), st.Truncated)
	}
	// A child with an invalid id or an absurd time is skipped and counted.
	out, st = parseTx(t, txPayload("", `{"span_id":"zz","op":"x"}`, `{"span_id":"1111111111111111","op":"x","start_timestamp":1e13,"timestamp":1e13}`))
	if len(out) != 1 || st.Invalid != 2 {
		t.Errorf("invalid children: len %d %+v", len(out), st)
	}
	// Long fields are clipped.
	out, _ = parseTx(t, txPayload("", txSpan("1111111111111111", txRoot, "x", strings.Repeat("é", 3000), 0, 0.1)))
	if len(out[1].Description) > 1000 {
		t.Errorf("description %d", len(out[1].Description))
	}
}

func TestParseTransactionRejects(t *testing.T) {
	for name, raw := range map[string]string{
		"not json":     `nope`,
		"long trace":   strings.Replace(string(txPayload("")), txTrace, strings.Repeat("a", 300), 1),
		"bad span id":  strings.Replace(string(txPayload("")), `"span_id":"`+txRoot+`"`, `"span_id":"xyz"`, 1),
		"no trace":     `{"type":"transaction","start_timestamp":1,"timestamp":2}`,
		"zero trace":   strings.Replace(string(txPayload("")), txTrace, strings.Repeat("0", 32), 1),
		"array":        `[]`,
		"too big":      string(txPayload("")) + strings.Repeat(" ", MaxEventBytes),
		"null context": `{"contexts":null}`,
	} {
		if _, err := ParseTransaction([]byte(raw), EventContext{}, txSite(), txNow); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	old := strings.NewReplacer(fmt.Sprintf("%.3f", txStart()), fmt.Sprintf("%.3f", float64(txNow.Add(-8*24*time.Hour).Unix())))
	if _, err := ParseTransaction([]byte(old.Replace(string(txPayload("")))), EventContext{}, txSite(), txNow); err != ErrTooOld {
		t.Errorf("old transaction: %v", err)
	}
	// 8 days old is accepted on a site that allows 13 days of late data.
	late := txSite()
	late.App.MaxReportAgeDays = 14
	if _, err := ParseTransaction([]byte(old.Replace(string(txPayload("")))), EventContext{}, late, txNow); err != nil {
		t.Errorf("late but within min(age,13): %v", err)
	}
}

func FuzzParseTransaction(f *testing.F) {
	f.Add(txPayload("", txSpan("1111111111111111", txRoot, "db", "select 1", 0, 0.1)))
	f.Add(txPayload(`,"measurements":{"app_start_cold":{"value":1}}`))
	f.Add([]byte(`{"contexts":{"trace":{"trace_id":"` + txTrace + `","span_id":"` + txRoot + `"}},"spans":[null,1,{"data":[]}]}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		out, err := ParseTransaction(raw, EventContext{}, txSite(), txNow)
		if err != nil {
			return
		}
		if len(out) == 0 || len(out) > MaxSpansPerItem+1 || !out[0].IsSegment {
			t.Fatalf("bad result len=%d", len(out))
		}
		for _, s := range out {
			if s.DurationUS < 0 || len(s.Description) > 1000 || len(s.Name) > 400 || len(s.Attrs) > 10 {
				t.Fatalf("bad span %+v", s)
			}
		}
	})
}

func TestDescribeParameterisesBrowserRequests(t *testing.T) {
	for _, c := range []struct{ op, in, want string }{
		{"browser.request", "GET /users/1234", "GET /users/*"},
		{"resource.fetch", "GET /users/1234?x=1", "GET /users/*"},
		{"resource.xhr", "POST https://api.example.com/v1/items/9f8e7d6c5b4a3210", "POST https://api.example.com/v1/items/*"},
		{"browser.request", "Request", "Request"},
		{"http.client", "GET /users/1234", "GET /users/*"}, // the existing output, unchanged
		{"browser.dns", "DNS lookup", "DNS lookup"},
	} {
		if got := describe(c.op, c.in); got != c.want {
			t.Errorf("describe(%q, %q) = %q, want %q", c.op, c.in, got, c.want)
		}
	}
}
