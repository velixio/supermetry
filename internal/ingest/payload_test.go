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
