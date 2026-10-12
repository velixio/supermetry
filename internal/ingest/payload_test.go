package ingest_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supermetry/internal/ingest"
)

func TestParsePayloadCollectionContractV1(t *testing.T) {
	tests := []struct {
		name    string
		fields  string
		wantID  string
		wantTS  int64
		wantVer int
		valid   bool
	}{
		{name: "legacy payload", valid: true},
		{name: "version one", fields: `,"v":1`, wantVer: 1, valid: true},
		{name: "bounded id and timestamp", fields: `,"id":"Az_09-x","ts":1791374400123`, wantID: "Az_09-x", wantTS: 1791374400123, valid: true},
		{name: "maximum id", fields: `,"id":"` + strings.Repeat("a", 64) + `"`, wantID: strings.Repeat("a", 64), valid: true},
		{name: "empty id", fields: `,"id":""`},
		{name: "overlong id", fields: `,"id":"` + strings.Repeat("a", 65) + `"`},
		{name: "id punctuation", fields: `,"id":"not.valid"`},
		{name: "version zero", fields: `,"v":0`},
		{name: "unknown version", fields: `,"v":2`},
		{name: "fractional timestamp", fields: `,"ts":1.5`},
		{name: "null timestamp", fields: `,"ts":null`},
		{name: "null version", fields: `,"v":null`},
		{name: "null id", fields: `,"id":null`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := ingest.ParsePayload([]byte(`{"site":"abc123","name":"pageview","url":"https://example.com/"` + tt.fields + `}`))
			if (err == nil) != tt.valid {
				t.Fatalf("ParsePayload error = %v, want valid=%v", err, tt.valid)
			}
			if !tt.valid {
				return
			}
			if payload.ID != tt.wantID {
				t.Fatalf("ID=%q, want %q", payload.ID, tt.wantID)
			}
			if tt.wantTS == 0 {
				if payload.TimestampMS != nil {
					t.Fatalf("TimestampMS=%v, want nil", payload.TimestampMS)
				}
			} else if payload.TimestampMS == nil || *payload.TimestampMS != tt.wantTS {
				t.Fatalf("TimestampMS=%v, want %d", payload.TimestampMS, tt.wantTS)
			}
			if tt.wantVer == 0 {
				if payload.Version != nil {
					t.Fatalf("Version=%v, want nil", payload.Version)
				}
			} else if payload.Version == nil || *payload.Version != tt.wantVer {
				t.Fatalf("Version=%v, want %d", payload.Version, tt.wantVer)
			}
		})
	}
}

func TestParsePayloadNormalizesWebVitalsProps(t *testing.T) {
	tests := []struct {
		name      string
		eventName string
		props     string
		want      map[string]string
	}{
		{
			name:      "keeps valid metrics and unrelated props",
			eventName: "Web Vitals",
			props:     `{"lcp":"2500","cls":"100","inp":"200","fcp":"1800","ttfb":"800","page":"/pricing"}`,
			want:      map[string]string{"lcp": "2500", "cls": "100", "inp": "200", "fcp": "1800", "ttfb": "800", "page": "/pricing"},
		},
		{
			name:      "keeps inclusive caps",
			eventName: "Web Vitals",
			props:     `{"lcp":"60000","cls":"10000","inp":"60000","fcp":"60000","ttfb":"60000"}`,
			want:      map[string]string{"lcp": "60000", "cls": "10000", "inp": "60000", "fcp": "60000", "ttfb": "60000"},
		},
		{
			name:      "drops negative malformed and over-cap metrics",
			eventName: "Web Vitals",
			props:     `{"lcp":"-1","cls":"10001","inp":"fast","fcp":"60001","ttfb":"-4","page":"/"}`,
			want:      map[string]string{"page": "/"},
		},
		{
			name:      "drops overlong metric before generic validation",
			eventName: "Web Vitals",
			props:     fmt.Sprintf(`{"lcp":%q,"fcp":%q,"ttfb":%q,"page":"/"}`, strings.Repeat("9", 2001), strings.Repeat("8", 2001), strings.Repeat("7", 2001)),
			want:      map[string]string{"page": "/"},
		},
		{
			name:      "does not normalize another event",
			eventName: "Checkout",
			props:     `{"lcp":"-1","cls":"10001","inp":"fast"}`,
			want:      map[string]string{"lcp": "-1", "cls": "10001", "inp": "fast"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := ingest.ParsePayload([]byte(fmt.Sprintf(
				`{"site":"abc123","name":%q,"url":"https://example.com/","props":%s}`,
				tt.eventName, tt.props,
			)))
			if err != nil {
				t.Fatalf("invalid vital prop rejected whole event: %v", err)
			}
			if !reflect.DeepEqual(payload.Props, tt.want) {
				t.Fatalf("props = %#v, want %#v", payload.Props, tt.want)
			}
		})
	}
}

func TestParsePayloadNormalizesScalarProps(t *testing.T) {
	payload, err := ingest.ParsePayload([]byte(
		`{"site":"abc123","name":"signup","url":"https://example.com/","props":{"plan":"pro","seats":3,"ratio":1.25,"ok":true,"disabled":false}}`,
	))
	if err != nil {
		t.Fatalf("scalar props rejected whole event: %v", err)
	}
	want := map[string]string{
		"plan": "pro", "seats": "3", "ratio": "1.25", "ok": "true", "disabled": "false",
	}
	if !reflect.DeepEqual(payload.Props, want) {
		t.Fatalf("props = %#v, want %#v", payload.Props, want)
	}
}

func TestParsePayloadKeepsFirstThirtyPropsSortedByKey(t *testing.T) {
	parts := make([]string, 0, 32)
	for i := 31; i >= 0; i-- {
		parts = append(parts, fmt.Sprintf(`"key-%02d":%d`, i, i))
	}
	payload, err := ingest.ParsePayload([]byte(
		`{"site":"abc123","name":"signup","url":"https://example.com/","props":{` + strings.Join(parts, ",") + `}}`,
	))
	if err != nil {
		t.Fatalf("over-cap props rejected whole event: %v", err)
	}
	if len(payload.Props) != 30 {
		t.Fatalf("props count = %d, want 30", len(payload.Props))
	}
	for i := 0; i < 30; i++ {
		key := fmt.Sprintf("key-%02d", i)
		if payload.Props[key] != fmt.Sprint(i) {
			t.Fatalf("props[%q] = %q, want %d", key, payload.Props[key], i)
		}
	}
	if _, ok := payload.Props["key-30"]; ok {
		t.Fatal("key-30 survived sorted truncation")
	}
}

func TestParsePayloadRejectsNonScalarProps(t *testing.T) {
	for _, props := range []string{`{"nested":{}}`, `{"items":[]}`, `{"missing":null}`} {
		if _, err := ingest.ParsePayload([]byte(
			`{"site":"abc123","name":"signup","url":"https://example.com/","props":` + props + `}`,
		)); err == nil {
			t.Fatalf("non-scalar props %s accepted", props)
		}
	}
}

func TestParsePayloadRejectsOverlongUnrelatedWebVitalsProp(t *testing.T) {
	_, err := ingest.ParsePayload([]byte(fmt.Sprintf(
		`{"site":"abc123","name":"Web Vitals","url":"https://example.com/","props":{"page":%q}}`,
		strings.Repeat("x", 2001),
	)))
	if err == nil {
		t.Fatal("overlong unrelated prop was accepted")
	}
}

func TestParsePayloadRevenue(t *testing.T) {
	tests := []struct {
		name     string
		amount   string
		currency string
		want     int64
	}{
		{name: "number", amount: `12.34`, currency: "USD", want: 12_340_000},
		{name: "string six decimals", amount: `"0.000001"`, currency: "EUR", want: 1},
		{name: "maximum", amount: `"1000000000000"`, currency: "JPY", want: 1_000_000_000_000_000_000},
		{name: "number exponent", amount: `1.234567e1`, currency: "GBP", want: 12_345_670},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := ingest.ParsePayload([]byte(fmt.Sprintf(
				`{"site":"abc123","name":"purchase","url":"https://example.com/","revenue":{"amount":%s,"currency":%q}}`,
				tt.amount, tt.currency,
			)))
			if err != nil {
				t.Fatal(err)
			}
			if payload.RevenueInvalid || payload.RevenueMicros != tt.want || payload.RevenueCurrency != tt.currency {
				t.Fatalf("revenue invalid=%v micros=%d currency=%q, want false/%d/%q", payload.RevenueInvalid, payload.RevenueMicros, payload.RevenueCurrency, tt.want, tt.currency)
			}
		})
	}
}

func TestParsePayloadDropsInvalidRevenue(t *testing.T) {
	tests := []struct {
		name     string
		amount   string
		currency string
	}{
		{name: "zero", amount: `0`, currency: "USD"},
		{name: "negative", amount: `-1`, currency: "USD"},
		{name: "over maximum", amount: `1000000000000.000001`, currency: "USD"},
		{name: "seven decimals", amount: `"1.0000001"`, currency: "USD"},
		{name: "sub-micro exponent", amount: `1e-7`, currency: "USD"},
		{name: "missing amount", amount: `null`, currency: "USD"},
		{name: "lowercase currency", amount: `1`, currency: "usd"},
		{name: "long currency", amount: `1`, currency: "USDX"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := ingest.ParsePayload([]byte(fmt.Sprintf(
				`{"site":"abc123","name":"purchase","url":"https://example.com/","revenue":{"amount":%s,"currency":%q}}`,
				tt.amount, tt.currency,
			)))
			if err != nil {
				t.Fatalf("invalid revenue rejected whole event: %v", err)
			}
			if !payload.RevenueInvalid || payload.RevenueMicros != 0 || payload.RevenueCurrency != "" {
				t.Fatalf("revenue invalid=%v micros=%d currency=%q, want true/0/empty", payload.RevenueInvalid, payload.RevenueMicros, payload.RevenueCurrency)
			}
		})
	}
}

func TestParsePayloadDropsMalformedRevenueShape(t *testing.T) {
	for _, tt := range []struct {
		name    string
		revenue string
	}{
		{name: "string", revenue: `"bad"`},
		{name: "array", revenue: `[]`},
		{name: "object amount", revenue: `{"amount":{},"currency":"USD"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := ingest.ParsePayload([]byte(fmt.Sprintf(
				`{"site":"abc123","name":"purchase","url":"https://example.com/","revenue":%s}`, tt.revenue,
			)))
			if err != nil {
				t.Fatalf("malformed revenue rejected whole event: %v", err)
			}
			if !payload.RevenueInvalid {
				t.Fatal("malformed revenue was not marked invalid")
			}
		})
	}
}

func TestParsePayloadIgnoresRevenueForNonCustomEvents(t *testing.T) {
	for _, name := range []string{"pageview", "engagement"} {
		t.Run(name, func(t *testing.T) {
			payload, err := ingest.ParsePayload([]byte(fmt.Sprintf(
				`{"site":"abc123","name":%q,"url":"https://example.com/","revenue":{"amount":"invalid","currency":"usd"}}`, name,
			)))
			if err != nil {
				t.Fatal(err)
			}
			if payload.RevenueInvalid || payload.RevenueMicros != 0 || payload.RevenueCurrency != "" {
				t.Fatalf("revenue invalid=%v micros=%d currency=%q, want ignored", payload.RevenueInvalid, payload.RevenueMicros, payload.RevenueCurrency)
			}
		})
	}
}

func TestParsePayloadIgnoresMalformedRevenueShapeOnPageview(t *testing.T) {
	payload, err := ingest.ParsePayload([]byte(
		`{"site":"abc123","name":"pageview","url":"https://example.com/","revenue":"bad"}`,
	))
	if err != nil {
		t.Fatalf("pageview revenue rejected whole event: %v", err)
	}
	if payload.RevenueInvalid {
		t.Fatal("pageview revenue was validated instead of ignored")
	}
}

func TestParsePayloadDefersProfileIdentityValidation(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		traits  string
	}{
		{name: "empty profile", profile: `""`},
		{name: "non-string profile", profile: `123`},
		{name: "long profile", profile: fmt.Sprintf("%q", strings.Repeat("p", 129))},
		{name: "too many traits", profile: `"customer-1"`, traits: traitObject(21)},
		{name: "nested traits", profile: `"customer-1"`, traits: `{"nested":{"key":"value"}}`},
		{name: "empty trait key", profile: `"customer-1"`, traits: `{"":"value"}`},
		{name: "long trait key", profile: `"customer-1"`, traits: fmt.Sprintf(`{%q:"value"}`, strings.Repeat("k", 65))},
		{name: "long trait value", profile: `"customer-1"`, traits: fmt.Sprintf(`{"key":%q}`, strings.Repeat("v", 257))},
		{name: "traits without profile", traits: `{"plan":"pro"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := ""
			if tt.profile != "" {
				identity = `,"profile_id":` + tt.profile
			}
			if tt.traits != "" {
				identity += `,"traits":` + tt.traits
			}
			eventName := "pageview"
			if tt.traits != "" {
				eventName = "identify"
			}
			body := fmt.Sprintf(`{"site":"abc123","name":%q,"url":"https://example.com/"%s}`, eventName, identity)
			payload, err := ingest.ParsePayload([]byte(body))
			if err != nil {
				t.Fatalf("structural payload rejected before site policy: %v", err)
			}
			if _, _, _, err := payload.Identity(true); err == nil {
				t.Fatal("enabled site accepted invalid identity")
			}
			if id, traits, supplied, err := payload.Identity(false); err != nil || id != "" || traits != nil || !supplied {
				t.Fatalf("disabled identity = %q/%v/%t/%v, want suppressed", id, traits, supplied, err)
			}
		})
	}
}

func TestParsePayloadHeatmap(t *testing.T) {
	tests := []struct {
		name string
		body string
		ok   bool
	}{
		{name: "valid boundaries", body: `{"site":"abc123","name":"heatmap","url":"https://example.com/pricing","clicks":[[0,0],[1000,100000]],"doc_h":100000,"vw":10000}`, ok: true},
		{name: "negative x", body: `{"site":"abc123","name":"heatmap","url":"https://example.com/","clicks":[[-1,0]],"doc_h":1,"vw":390}`},
		{name: "x above maximum", body: `{"site":"abc123","name":"heatmap","url":"https://example.com/","clicks":[[1001,0]],"doc_h":1,"vw":390}`},
		{name: "negative y", body: `{"site":"abc123","name":"heatmap","url":"https://example.com/","clicks":[[1,-1]],"doc_h":1,"vw":390}`},
		{name: "y above maximum", body: `{"site":"abc123","name":"heatmap","url":"https://example.com/","clicks":[[1,100001]],"doc_h":1,"vw":390}`},
		{name: "document above maximum", body: `{"site":"abc123","name":"heatmap","url":"https://example.com/","clicks":[[1,1]],"doc_h":100001,"vw":390}`},
		{name: "viewport above maximum", body: `{"site":"abc123","name":"heatmap","url":"https://example.com/","clicks":[[1,1]],"doc_h":1,"vw":10001}`},
		{name: "empty clicks", body: `{"site":"abc123","name":"heatmap","url":"https://example.com/","clicks":[],"doc_h":1,"vw":390}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := ingest.ParsePayload([]byte(tt.body))
			if (err == nil) != tt.ok {
				t.Fatalf("ParsePayload error = %v, want valid=%v", err, tt.ok)
			}
			if tt.ok && (len(payload.Clicks) != 2 || payload.DocHeight != 100000 || payload.ViewportWidth != 10000) {
				t.Fatalf("heatmap payload = %#v", payload)
			}
		})
	}
}

func TestParsePayloadAcceptsBoundedProfileIdentity(t *testing.T) {
	body := fmt.Sprintf(
		`{"site":"abc123","name":"identify","url":"https://example.com/","profile_id":%q,"traits":{%q:%q}}`,
		strings.Repeat("p", 128), strings.Repeat("k", 64), strings.Repeat("v", 256),
	)
	payload, err := ingest.ParsePayload([]byte(body))
	if err != nil {
		t.Fatalf("bounded profile identity rejected: %v", err)
	}
	id, traits, supplied, err := payload.Identity(true)
	if err != nil || id != strings.Repeat("p", 128) || len(traits) != 1 || !supplied {
		t.Fatalf("identity = %q/%v/%t/%v", id, traits, supplied, err)
	}
}

func traitObject(count int) string {
	parts := make([]string, count)
	for i := range count {
		parts[i] = fmt.Sprintf(`"k%d":"v"`, i)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// The tracker and the profiles.external_id CHECK count characters, so the
// ingest caps must count characters too; a byte cap would reject every later
// event from a page that identified a user with a non-ASCII identifier.
func TestParsePayloadCountsProfileCharactersNotBytes(t *testing.T) {
	body := fmt.Sprintf(
		`{"site":"abc123","name":"identify","url":"https://example.com/","profile_id":%q,"traits":{%q:%q}}`,
		strings.Repeat("é", 128), strings.Repeat("ключ", 16), strings.Repeat("ü", 256),
	)
	payload, err := ingest.ParsePayload([]byte(body))
	if err != nil {
		t.Fatalf("payload rejected: %v", err)
	}
	if _, _, _, err := payload.Identity(true); err != nil {
		t.Fatalf("multibyte identity within character caps rejected: %v", err)
	}
	long := fmt.Sprintf(`{"site":"abc123","name":"identify","url":"https://example.com/","profile_id":%q}`, strings.Repeat("é", 129))
	payload, err = ingest.ParsePayload([]byte(long))
	if err != nil {
		t.Fatalf("payload rejected: %v", err)
	}
	if _, _, _, err := payload.Identity(true); err == nil {
		t.Fatal("129-character identifier accepted")
	}
}

func TestParsePayloadHeatmapRejectsMoreThanTwoHundredClicks(t *testing.T) {
	clicks := strings.Repeat(`[1,1],`, 200) + `[1,1]`
	_, err := ingest.ParsePayload([]byte(fmt.Sprintf(
		`{"site":"abc123","name":"heatmap","url":"https://example.com/","clicks":[%s],"doc_h":1,"vw":390}`,
		clicks,
	)))
	if err == nil {
		t.Fatal("201 heatmap clicks were accepted")
	}
}
