package health_test

import (
	"bytes"
	"strings"
	"testing"

	"panel/internal/health"
)

func TestPrometheusTextFormat(t *testing.T) {
	health.IngestAccepted.Add(3)
	health.BotsFiltered[1].Add(2)
	var b bytes.Buffer
	health.WritePrometheus(&b)
	for _, want := range []string{
		"# TYPE panel_ingest_accepted_total counter",
		"panel_ingest_accepted_total 3",
		`panel_bots_filtered_total{reason="ua"} 2`,
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in\n%s", want, b.String())
		}
	}
}
