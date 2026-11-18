package attrs_test

import (
	"fmt"
	"strings"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"

	"supermetry/internal/otlpingest/attrs"
)

func sensitive(k string) bool { return strings.Contains(k, "token") || k == "authorization" }

func scrub(s string) string { return strings.ReplaceAll(s, "jane@example.com", "[email]") }

func TestLogCapsCountSizeAndKeys(t *testing.T) {
	cfg := attrs.Logs(scrub, sensitive)
	set := cfg.NewSet()
	for i := 0; i < 40; i++ {
		set.Add(fmt.Sprintf("k%02d", i), "v")
	}
	set.Add(strings.Repeat("k", 129), "too long a key")
	got, dropped := set.Finish()
	if len(got) != 32 || dropped != 8 {
		t.Fatalf("kept %d dropped %d, want 32 and 8", len(got), dropped)
	}
	// Sorted-key order: k00..k31 survive.
	if _, ok := got["k31"]; !ok {
		t.Error("k31 dropped")
	}
	if _, ok := got["k32"]; ok {
		t.Error("k32 kept")
	}

	set = cfg.NewSet()
	set.Add("big", strings.Repeat("é", 2000)) // 4000 bytes
	got, _ = set.Finish()
	if v := got["big"].(string); len(v) != 1024 || v[len(v)-2:] != "éé"[:2] {
		t.Errorf("value length %d, want 1024 cut on a rune boundary", len(v))
	}
}

func TestLogEncodedSizeCap(t *testing.T) {
	cfg := attrs.Logs(nil, nil)
	set := cfg.NewSet()
	for i := 0; i < 8; i++ {
		set.Add(fmt.Sprintf("a%d", i), strings.Repeat("x", 1000))
	}
	got, dropped := set.Finish()
	// Each pair encodes to 1008 bytes: 4 fit in 4,096, the other 4 are dropped
	// in sorted-key order.
	if len(got) != 4 || dropped != 4 {
		t.Fatalf("kept %d dropped %d, want 4 and 4", len(got), dropped)
	}
	for _, k := range []string{"a0", "a1", "a2", "a3"} {
		if _, ok := got[k]; !ok {
			t.Errorf("%s dropped", k)
		}
	}
}

func TestPrivacyKeyRules(t *testing.T) {
	cfg := attrs.Logs(scrub, sensitive)
	set := cfg.NewSet()
	for _, k := range []string{"client.address", "net.peer.ip", "net.sock.peer.addr", "http.client_ip", "source.address",
		"user.email", "user.name", "user.full_name", "enduser.role", "enduser.scope", "api_token", "Authorization"} {
		set.Add(k, "x")
	}
	set.Add("enduser.id", "e-9")
	set.Add("http.route", "/pay")
	got, _ := set.Finish()
	if len(got) != 1 || got["http.route"] != "/pay" {
		t.Fatalf("attributes = %v, want only http.route", got)
	}
	if set.UserID() != "e-9" {
		t.Errorf("user id = %q", set.UserID())
	}
	set = cfg.NewSet()
	set.Add("user.id", int64(42))
	if got, _ := set.Finish(); len(got) != 0 || set.UserID() != "42" {
		t.Errorf("user.id: attributes %v id %q", got, set.UserID())
	}
}

func TestNULAndInvalidUTF8Stripped(t *testing.T) {
	cfg := attrs.Logs(nil, nil)
	set := cfg.NewSet()
	set.Add("k\x00ey", "va\x00lue\xff")
	set.Add("arr", []any{"a\x00b", map[string]any{"c\x00": "d"}})
	got, _ := set.Finish()
	if got["key"] != "value�" {
		t.Errorf("value = %q", got["key"])
	}
	if got["arr"] != `["ab",{"c":"d"}]` {
		t.Errorf("array = %q", got["arr"])
	}
	if s, cut := cfg.Text("a\x00b", 10); s != "ab" || cut {
		t.Errorf("text = %q cut=%v", s, cut)
	}
	if s, cut := cfg.Text(strings.Repeat("é", 10), 5); s != "éé" || !cut {
		t.Errorf("text = %q cut=%v, want a cut on a rune boundary", s, cut)
	}
}

func TestComplexValuesJSONEncodedAndCut(t *testing.T) {
	cfg := attrs.Logs(nil, nil)
	set := cfg.NewSet()
	set.Add("m", map[string]any{"a": int64(1), "b": []any{true, "x"}})
	long := make([]any, 400)
	for i := range long {
		long[i] = "abcdefgh"
	}
	set.Add("long", long)
	got, _ := set.Finish()
	if got["m"] != `{"a":1,"b":[true,"x"]}` {
		t.Errorf("m = %q", got["m"])
	}
	if len(got["long"].(string)) != 1024 {
		t.Errorf("long = %d bytes, want 1024", len(got["long"].(string)))
	}
}

func TestMetricRulesStringifyAndSkipComplex(t *testing.T) {
	cfg := attrs.Metrics(sensitive)
	set := cfg.NewSet()
	set.AddOTLP([]*commonpb.KeyValue{
		{Key: "n", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 7}}},
		{Key: "b", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}},
		{Key: "arr", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{}}}},
		{Key: "long", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: strings.Repeat("z", 300)}}},
		{Key: "user.id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "u"}}},
	})
	got, dropped := set.Finish()
	m := attrs.Strings(got)
	if dropped != 0 || len(m) != 3 || m["n"] != "7" || m["b"] != "true" || len(m["long"]) != 256 {
		t.Fatalf("attributes = %v dropped %d", m, dropped)
	}
	// 20 keys over the cap of 16.
	set = cfg.NewSet()
	for i := 0; i < 20; i++ {
		set.Add(fmt.Sprintf("k%02d", i), "v")
	}
	if got, dropped := set.Finish(); len(got) != 16 || dropped != 4 {
		t.Fatalf("kept %d dropped %d", len(got), dropped)
	}
}
