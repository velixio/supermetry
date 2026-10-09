package query

import "testing"

func TestNormalizedCacheQueryIncludesProjectCurrency(t *testing.T) {
	usd := normalizedCacheQuery(Normalized{RevenueCurrency: "USD"})
	eur := normalizedCacheQuery(Normalized{RevenueCurrency: "EUR"})
	if usd == eur {
		t.Fatal("cache serialization ignored project currency")
	}
}
