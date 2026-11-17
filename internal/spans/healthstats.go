package spans

import "supermetry/internal/health"

// HealthStats exports span drop counts as health counters. Names it does not
// know are ignored.
type HealthStats struct{}

// Add implements Stats.
func (HealthStats) Add(name string, n int) {
	if n <= 0 {
		return
	}
	switch name {
	case "span_queue_full":
		health.SpanQueueFull.Add(uint64(n))
	case "span_rate_limited":
		health.SpanRateLimited.Add(uint64(n))
	case "span_write_failed":
		health.SpanWriteFailed.Add(uint64(n))
	case "span_no_partition":
		health.SpanNoPartition.Add(uint64(n))
	}
}
