import unittest
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import evaluate


class EvaluateTests(unittest.TestCase):
    def setUp(self):
        self.thresholds = {
            "startup_ms": 1000,
            "ingest_rate": 10000,
            "ingest_p99_ms": 50,
            "dashboard_p95_ms": 300,
            "binary_mb": 30,
            "tracker_gzip_bytes": 1024,
            "S2_p95_ms": {"S30": 72, "S90": 92},
            "S3_p95_ms": {"S30": [570, 386, 290], "S90": [1000, 837, 792]},
            "S4_p95_ms": {"S30": 375},
            "F4_p95_ms": {"S30": 49},
            "R1_mb": 50,
            "R2_mb": 150,
            "C1_mismatches": 0,
            "regression_tolerance": 0.2,
        }
        self.measured = {
            "startup_ms": 100,
            "S1": {"ok_rate": 10000, "p99_ms": 10, "sent": 600000, "ok_2xx": 600000, "client_overflow": 0},
            "dashboard_p95_ms": {"S30": 100, "S90": 120},
            "S2_p95_ms": {"S30": 60, "S90": 80},
            "S3_p95_ms": {"S30": [500, 300, 200], "S90": [900, 700, 700]},
            "S4_p95_ms": {"S30": 300},
            "F4_p95_ms": {"S30": 40},
            "R1_mb": 30,
            "R2_mb": 100,
            "C1_mismatches": 0,
            "binary_mb": 20,
            "tracker_gzip_bytes": 900,
        }

    def test_all_must_checks_pass_at_valid_values(self):
        checks = evaluate.build_checks(self.measured, self.thresholds)
        self.assertTrue(checks)
        self.assertTrue(all(check["pass"] for check in checks), checks)

    def test_ingest_requires_rate_latency_and_zero_loss(self):
        self.measured["S1"].update(ok_rate=9999, p99_ms=51, ok_2xx=599999, client_overflow=1)
        checks = {check["id"]: check for check in evaluate.build_checks(self.measured, self.thresholds)}
        self.assertFalse(checks["S1"]["pass"])
        self.assertIn("0 drops", checks["S1"]["threshold"])

    def test_strict_threshold_does_not_accept_boundary(self):
        self.measured["startup_ms"] = 1000
        checks = {check["id"]: check for check in evaluate.build_checks(self.measured, self.thresholds)}
        self.assertFalse(checks["STARTUP"]["pass"])


if __name__ == "__main__":
    unittest.main()
