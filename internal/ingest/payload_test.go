package ingest_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"panel/internal/ingest"
)

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
			props:     `{"lcp":"2500","cls":"100","inp":"200","page":"/pricing"}`,
			want:      map[string]string{"lcp": "2500", "cls": "100", "inp": "200", "page": "/pricing"},
		},
		{
			name:      "keeps inclusive caps",
			eventName: "Web Vitals",
			props:     `{"lcp":"60000","cls":"10000","inp":"60000"}`,
			want:      map[string]string{"lcp": "60000", "cls": "10000", "inp": "60000"},
		},
		{
			name:      "drops negative malformed and over-cap metrics",
			eventName: "Web Vitals",
			props:     `{"lcp":"-1","cls":"10001","inp":"fast","page":"/"}`,
			want:      map[string]string{"page": "/"},
		},
		{
			name:      "drops overlong metric before generic validation",
			eventName: "Web Vitals",
			props:     fmt.Sprintf(`{"lcp":%q,"page":"/"}`, strings.Repeat("9", 2001)),
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
