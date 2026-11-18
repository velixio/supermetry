package obsmetrics

import "supermetry/internal/health"

// HealthStats exports metric drop counts as health counters. Names it does not
// know are ignored.
type HealthStats struct{}

// Add implements Stats.
func (HealthStats) Add(name string, n int) {
	if n <= 0 {
		return
	}
	switch name {
	case "metric_queue_full":
		health.MetricQueueFull.Add(uint64(n))
	case "metric_write_failed":
		health.MetricWriteFailed.Add(uint64(n))
	case "metric_no_partition":
		health.MetricNoPartition.Add(uint64(n))
	case "metric_series_limit":
		health.MetricSeriesLimit.Add(uint64(n))
	case "metric_unsupported":
		health.MetricUnsupported.Add(uint64(n))
	}
}
