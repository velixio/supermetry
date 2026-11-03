package stripe

import (
	"errors"
	"math"
	"strings"
)

var errAmount = errors.New("stripe amount is out of range")

// zeroDecimal lists the currencies whose Stripe amounts are whole units.
// ISK, HUF, TWD and UGX are absent on purpose: Stripe's API still expresses
// them with two decimals (an amount of 500 is 5 units).
var zeroDecimal = map[string]bool{
	"BIF": true, "CLP": true, "DJF": true, "GNF": true, "JPY": true, "KMF": true,
	"KRW": true, "MGA": true, "PYG": true, "RWF": true, "VND": true, "VUV": true,
	"XAF": true, "XOF": true, "XPF": true,
}

// threeDecimal lists the currencies whose Stripe amounts are thousandths.
var threeDecimal = map[string]bool{"BHD": true, "JOD": true, "KWD": true, "OMR": true, "TND": true}

// CurrencyDecimals returns the number of decimals Stripe uses for a currency
// amount in its API (the "minor unit"). The code may be in any case.
func CurrencyDecimals(currency string) int {
	code := strings.ToUpper(currency)
	switch {
	case zeroDecimal[code]:
		return 0
	case threeDecimal[code]:
		return 3
	default:
		return 2
	}
}

// MinorToMicros converts a Stripe amount in the currency's minor unit to
// millionths of the major unit, which is how Supermetry stores revenue. It
// returns the upper-case ISO code. Negative amounts and overflow are errors.
func MinorToMicros(amount int64, currency string) (int64, string, error) {
	code := strings.ToUpper(strings.TrimSpace(currency))
	if len(code) != 3 || strings.Trim(code, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return 0, "", errors.New("stripe currency is not an ISO 4217 code")
	}
	if amount < 0 {
		return 0, "", errAmount
	}
	factor := int64(1)
	for range 6 - CurrencyDecimals(code) {
		factor *= 10
	}
	if amount > math.MaxInt64/factor {
		return 0, "", errAmount
	}
	return amount * factor, code, nil
}
