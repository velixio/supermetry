package health

import "fmt"

var dimensionDictionaryKinds = [...]string{
	"path", "referrer", "source", "medium", "campaign", "utm_content",
	"utm_term", "event_name", "prop_key", "browser", "os", "region", "city",
	"browser_version", "os_version", "language", "hostname",
}

var dimensionDictionaryFill [len(dimensionDictionaryKinds)]Gauge

// SetDimensionDictionaryFill records the fraction of one dictionary kind's
// configured capacity that is currently cached.
func SetDimensionDictionaryFill(kind int, ratio float64) {
	if kind < 0 || kind >= len(dimensionDictionaryFill) {
		return
	}
	dimensionDictionaryFill[kind].Store(ratio)
}

func addDimensionDictionaryFill(snapshot map[string]float64) {
	for kind, name := range dimensionDictionaryKinds {
		snapshot[fmt.Sprintf(`dimension_dictionary_fill_ratio{kind=%q}`, name)] = dimensionDictionaryFill[kind].Load()
	}
}
