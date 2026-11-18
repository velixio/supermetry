package sentryingest

import (
	"fmt"
	"strings"
	"testing"

	"supermetry/internal/spans"
)

func v2Attr(kv ...string) string {
	var parts []string
	for i := 0; i+1 < len(kv); i += 2 {
		parts = append(parts, fmt.Sprintf(`%q:{"type":"string","value":%q}`, kv[i], kv[i+1]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func v2Item(id, parent string, segment bool, name string, off, dur float64, attrs string) string {
	s := txStart() + off
	p := ""
	if parent != "" {
		p = fmt.Sprintf(`"parent_span_id":%q,`, parent)
	}
	return fmt.Sprintf(`{"trace_id":"%s","span_id":"%s",%s"name":%q,"status":"ok","is_segment":%v,"start_timestamp":%.3f,"end_timestamp":%.3f,"attributes":%s}`,
		txTrace, id, p, name, segment, s, s+dur, attrs)
}

func v2Container(items ...string) []byte {
	return []byte(`{"version":2,"ingest_settings":{"infer_ip":"auto"},"items":[` + strings.Join(items, ",") + `]}`)
}

func parseV2(t *testing.T, raw []byte) ([]spans.Span, *SpanStats) {
	t.Helper()
	var st SpanStats
	out, err := parseSpanV2(raw, EventContext{}, txSite(), txNow, &st)
	if err != nil {
		t.Fatal(err)
	}
	return out, &st
}

func TestParseSpanV2Segment(t *testing.T) {
	out, _ := parseV2(t, v2Container(
		v2Item(txRoot, "", true, "GET /api/items", 0, 0.3, v2Attr("sentry.op", "http.server", "sentry.release", "app@2.0", "sentry.environment", "prod", "sentry.segment.name", "GET /api/items", "http.response.status_code", "200")),
		v2Item("1111111111111111", txRoot, false, "select * from users where email = 'a@b.c'", 0.01, 0.05, v2Attr("sentry.op", "db.query", "db.system", "postgresql", "user.email", "a@b.c")),
		v2Item("2222222222222222", "1111111111111111", false, "GET https://api.example.com/x/12345?token=sk_live", 0.07, 0.1, v2Attr("sentry.op", "http.client", "sentry.segment.id", txRoot)),
	))
	if len(out) != 3 {
		t.Fatalf("got %d", len(out))
	}
	seg, db, http := out[0], out[1], out[2]
	if !seg.IsSegment || seg.Name != "GET /api/items" || seg.Op != "http.server" || seg.Release != "app@2.0" || seg.Environment != "prod" || seg.DurationUS != 300_000 || seg.ParentID != nil {
		t.Errorf("segment %+v", seg)
	}
	if db.IsSegment || db.SegmentID != seg.SpanID || db.Op != "db.query" || db.Description != "select * from users where email = ?" || db.Attrs["db.system"] != "postgresql" || len(db.Attrs) != 1 {
		t.Errorf("db %+v", db)
	}
	if db.Release != "app@2.0" || db.Environment != "prod" {
		t.Errorf("children inherit the segment's release: %+v", db)
	}
	if http.Description != "GET https://api.example.com/x/*" || http.SegmentID != seg.SpanID {
		t.Errorf("http %+v", http)
	}
}

func TestParseSpanV2ParentlessIsSegmentWhenNoneFlagged(t *testing.T) {
	out, _ := parseV2(t, v2Container(
		v2Item(txRoot, "", false, "root", 0, 0.3, "{}"),
		v2Item("1111111111111111", txRoot, false, "child", 0.01, 0.05, "{}"),
	))
	if !out[0].IsSegment || out[1].IsSegment || out[1].SegmentID != out[0].SpanID {
		t.Errorf("%+v", out)
	}
}

func TestParseSpanV2Limits(t *testing.T) {
	var items []string
	for i := range 1500 {
		items = append(items, v2Item(fmt.Sprintf("%016x", i+1), "", false, "s", 0, 0.01, "{}"))
	}
	out, st := parseV2(t, v2Container(items...))
	if len(out) != MaxSpansPerItem || st.Truncated != 500 {
		t.Errorf("len %d truncated %d", len(out), st.Truncated)
	}
	// Bad ids, negative durations and a 300-character trace id never panic.
	bad := strings.Replace(v2Item("3333333333333333", "", false, "s", 0, 0.01, "{}"), txTrace, strings.Repeat("a", 300), 1)
	neg := v2Item("4444444444444444", "", false, "s", 1, -0.5, "{}")
	out, st = parseV2(t, v2Container(bad, `{"span_id":"zz"}`, `null`, `5`, v2Item("5555555555555555", "", false, "ok", 0, 0.01, "{}"), neg))
	if len(out) != 2 || st.Invalid != 4 {
		t.Errorf("len %d %+v", len(out), st)
	}
	for _, s := range out {
		if s.DurationUS < 0 {
			t.Errorf("negative duration %+v", s)
		}
	}
}

func TestParseSpanV2Rejects(t *testing.T) {
	for name, raw := range map[string]string{
		"not json": `x`, "array": `[]`, "no items": `{"version":2}`, "empty items": `{"items":[]}`,
		"too big": `{"items":[]}` + strings.Repeat(" ", MaxEventBytes),
	} {
		if _, err := ParseSpanV2([]byte(raw), EventContext{}, txSite(), txNow); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestParseSpanV2TooOld(t *testing.T) {
	old := strings.NewReplacer(fmt.Sprintf("%.3f", txStart()), "1700000000.000")
	_, err := ParseSpanV2([]byte(old.Replace(string(v2Container(v2Item(txRoot, "", true, "x", 0, 0.1, "{}"))))), EventContext{}, txSite(), txNow)
	if err != ErrTooOld {
		t.Errorf("err = %v", err)
	}
}

func FuzzParseSpanV2(f *testing.F) {
	f.Add(v2Container(v2Item(txRoot, "", true, "GET /", 0, 0.3, v2Attr("sentry.op", "http.server")), v2Item("1111111111111111", txRoot, false, "select 1", 0, 0.1, "{}")))
	f.Add([]byte(`{"items":[{"attributes":[],"span_id":"1111111111111111","trace_id":"` + txTrace + `","start_timestamp":"x"}]}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		out, err := ParseSpanV2(raw, EventContext{}, txSite(), txNow)
		if err != nil {
			return
		}
		if len(out) > MaxSpansPerItem {
			t.Fatalf("len %d", len(out))
		}
		for _, s := range out {
			if s.DurationUS < 0 || len(s.Description) > 1000 || len(s.Name) > 400 || len(s.Attrs) > 10 {
				t.Fatalf("bad span %+v", s)
			}
		}
	})
}

// The names below come from a real @sentry/browser 11.13.0 pageload and a
// @sentry/node 11.13.0 route span: the SDK marks the naming source in
// `sentry.segment.name.source` (and `sentry.source`), and a pageload without a
// router is just called "Pageload" with the page in `url.path`.
func TestParseSpanV2PageloadNamedAfterURLPath(t *testing.T) {
	out, _ := parseV2(t, v2Container(
		v2Item(txRoot, "", true, "Pageload", 0, 0.6, v2Attr("sentry.op", "pageload", "sentry.segment.name", "Pageload", "sentry.segment.name.source", "url", "url.path", "/users/1234/orders", "url.full", "http://x.example.com/users/1234/orders?token=abc")),
	))
	if len(out) != 1 || out[0].Name != "/users/*/orders" {
		t.Fatalf("pageload segment = %+v, want the parameterised url path", out)
	}
}

func TestParseSpanV2URLSourceIsParameterised(t *testing.T) {
	out, _ := parseV2(t, v2Container(
		v2Item(txRoot, "", true, "/orders/9f8b7c6d5e4f3a2b", 0, 0.1, v2Attr("sentry.op", "pageload", "sentry.segment.name.source", "url")),
	))
	if len(out) != 1 || out[0].Name != "/orders/*" {
		t.Fatalf("url-source name = %+v", out)
	}
}

func TestParseSpanV2RouteNameKept(t *testing.T) {
	out, _ := parseV2(t, v2Container(
		v2Item(txRoot, "", true, "GET /users/:id", 0, 0.1, v2Attr("sentry.op", "http.server", "sentry.segment.name.source", "custom", "sentry.source", "route", "url.path", "/users/42")),
	))
	if len(out) != 1 || out[0].Name != "GET /users/:id" {
		t.Fatalf("route name = %+v", out)
	}
}
