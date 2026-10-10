package query

import (
	"testing"

	"supermetry/internal/api"
)

func TestImportedQueryEligibleForEventAndGoalDimensions(t *testing.T) {
	tests := []struct {
		name      string
		dimension string
		metric    api.Metric
	}{
		{name: "event occurrences", dimension: "event", metric: api.Events},
		{name: "goal conversions", dimension: "goal", metric: api.Conversions},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := Normalized{IncludeImported: true, Dimensions: []string{tt.dimension}, Metrics: []api.Metric{tt.metric}}
			if !importedQueryEligible(req) {
				t.Fatalf("importedQueryEligible(%s, %s) = false, want true", tt.dimension, tt.metric)
			}
		})
	}
}
