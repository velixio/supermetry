import json
import unittest

from soak import Sample, downsample, parse_sample, percentile, slope_per_hour, verdicts


def sample(minute: int, *, rss_kib: int = 100_000, p99_ms: float = 20.0) -> Sample:
    return Sample(
        elapsed_s=minute * 60,
        rss_kib=rss_kib,
        cpu_seconds=float(minute),
        goroutines=None,
        queue_depth=0,
        accepted=minute * 100,
        dropped=0,
        invalid=0,
        database_bytes=minute * 1_000,
        stored=minute * 100,
        p50_ms=5.0,
        p95_ms=10.0,
        p99_ms=p99_ms,
        non_2xx=0,
        server_5xx=0,
    )


class PercentileTests(unittest.TestCase):
    def test_nearest_rank_uses_literal_order_statistics(self) -> None:
        self.assertEqual(percentile([40.0, 10.0, 30.0, 20.0], 0.50), 20.0)
        self.assertEqual(percentile([40.0, 10.0, 30.0, 20.0], 0.99), 40.0)

    def test_empty_values_raise_clear_error(self) -> None:
        with self.assertRaisesRegex(ValueError, "percentile requires at least one value"):
            percentile([], 0.99)


class SampleTests(unittest.TestCase):
    def test_parse_sample_accepts_json_line(self) -> None:
        expected = sample(1)
        self.assertEqual(parse_sample(json.dumps(expected.to_dict())), expected)

    def test_downsample_keeps_worst_latency_and_latest_counters(self) -> None:
        samples = [sample(i, p99_ms=80.0 if i == 7 else 20.0) for i in range(1, 17)]
        rows = downsample(samples, interval_seconds=15 * 60)
        self.assertEqual(len(rows), 2)
        self.assertEqual(rows[0].p99_ms, 80.0)
        self.assertEqual(rows[0].accepted, 1_400)
        self.assertEqual(rows[1].accepted, 1_600)


class VerdictTests(unittest.TestCase):
    def test_flat_rss_series_has_zero_slope_and_passes(self) -> None:
        samples = [sample(i * 10) for i in range(10)]
        self.assertAlmostEqual(slope_per_hour(samples), 0.0)
        self.assertTrue(verdicts(samples, accepted=900, stored=900)["rss_growth"].passed)

    def test_steadily_growing_rss_fails(self) -> None:
        samples = [sample(i * 10, rss_kib=100_000 + i * 10_000) for i in range(10)]
        self.assertGreater(slope_per_hour(samples), 5.0)
        self.assertFalse(verdicts(samples, accepted=900, stored=900)["rss_growth"].passed)

    def test_p99_spike_in_one_five_minute_window_fails(self) -> None:
        samples = [sample(i, p99_ms=150.0 if i == 7 else 20.0) for i in range(1, 11)]
        self.assertFalse(verdicts(samples, accepted=1_000, stored=1_000)["p99_latency"].passed)

    def test_stored_mismatch_fails(self) -> None:
        result = verdicts([sample(1)], accepted=100, stored=99)
        self.assertFalse(result["stored_equals_accepted"].passed)

    def test_empty_samples_raise_clear_error(self) -> None:
        with self.assertRaisesRegex(ValueError, "at least one soak sample is required"):
            verdicts([], accepted=0, stored=0)


if __name__ == "__main__":
    unittest.main()
