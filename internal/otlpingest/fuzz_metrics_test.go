package otlpingest

import (
	"os"
	"testing"

	"supermetry/internal/obsmetrics"
)

// The metrics decoders face the public internet: any byte string must return
// an error or a request that Normalise survives, never panic.

func FuzzDecodeMetrics(f *testing.F) {
	f.Add(javaAgent(3), false)
	f.Add([]byte{}, false)
	f.Add([]byte{0x0a, 0xff, 0xff, 0xff, 0xff, 0x0f}, false)
	f.Add([]byte(`{}`), true)
	f.Add([]byte(`{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"m","sum":{"aggregationTemporality":"x","dataPoints":[{"asInt":"-1","timeUnixNano":"zz"}]}}]}]}]}`), true)
	f.Add([]byte(`{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"h","histogram":{"dataPoints":[{"sum":"NaN","explicitBounds":["Infinity"],"bucketCounts":["1","2"]}]}}]}]}]}`), true)
	if b, err := os.ReadFile("testdata/metrics/python_sdk.json"); err == nil {
		f.Add(b, true)
	}
	f.Fuzz(func(t *testing.T, b []byte, asJSON bool) {
		var req *MetricsRequest
		var err error
		if asJSON {
			req, err = DecodeMetricsJSON(b)
		} else {
			req, err = DecodeMetricsProto(b)
		}
		if err != nil {
			return
		}
		obsmetrics.CountPoints(req.ResourceMetrics)
		obsmetrics.Normalise(req.ResourceMetrics, 1, testNow, nil)
	})
}
