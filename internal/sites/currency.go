package sites

type RevenueCurrencyState string

const (
	RevenueCurrencyUnconfigured RevenueCurrencyState = "unconfigured"
	RevenueCurrencyConfigured   RevenueCurrencyState = "configured"
	RevenueCurrencyConflict     RevenueCurrencyState = "conflict"
)

func ValidCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for i := range len(value) {
		if value[i] < 'A' || value[i] > 'Z' {
			return false
		}
	}
	return true
}
