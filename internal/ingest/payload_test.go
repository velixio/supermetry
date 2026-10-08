package ingest_test

import (
	"fmt"
	"testing"

	"panel/internal/ingest"
)

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
