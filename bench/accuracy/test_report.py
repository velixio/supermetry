import unittest

from report import render_report


class ReportTest(unittest.TestCase):
    def test_report_sums_mismatches_and_records_optional_layer(self):
        hot = {"queries": 2, "sources_used": ["hot"], "mismatches": []}
        closed = {"queries": 2, "sources_used": ["closed", "rollup"], "mismatches": [{"metric": "totals.visitors", "expected": 2, "actual": 3, "query": {"site_id": 1}}]}
        report = render_report("20261008T000000Z", hot, closed, "SKIPPED: Docker unavailable")
        self.assertIn("Mismatch count: **1**", report)
        self.assertIn("SKIPPED: Docker unavailable", report)
        self.assertIn("totals.visitors", report)
        self.assertIn("compare-live.py", report)
        self.assertIn("bulk-load UTM source", report)
        self.assertIn("empty UTM sentinel", report)


if __name__ == "__main__":
    unittest.main()
