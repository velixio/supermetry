import unittest

from compare_live import compare_snapshots, render_delta_table


class CompareLiveTest(unittest.TestCase):
    def test_tolerances_distinguish_percent_from_percentage_points(self):
        panel = {
            "totals": {"visitors": 100, "visits": 120, "pageviews": 250, "events": 20, "bounce_rate": 55.0},
            "pages": ["/", "/pricing"],
            "sources": ["Direct / None", "Google"],
        }
        plausible = {
            "totals": {"visitors": 102, "visits": 118, "pageviews": 250, "events": 20, "bounce_rate": 56.9},
            "pages": ["/pricing", "/"],
            "sources": ["Google", "Direct / None"],
        }
        rows = compare_snapshots(panel, plausible)
        self.assertTrue(all(row["pass"] for row in rows))
        self.assertIn("bounce_rate", render_delta_table(rows))

    def test_exact_and_top_ten_mismatches_fail(self):
        panel = {
            "totals": {"visitors": 100, "visits": 100, "pageviews": 9, "events": 2, "bounce_rate": 50},
            "pages": ["/a"],
            "sources": ["Google"],
        }
        plausible = {
            "totals": {"visitors": 100, "visits": 100, "pageviews": 10, "events": 3, "bounce_rate": 50},
            "pages": ["/b"],
            "sources": ["Google"],
        }
        failed = {row["metric"] for row in compare_snapshots(panel, plausible) if not row["pass"]}
        self.assertEqual(failed, {"pageviews", "events", "top_pages"})


if __name__ == "__main__":
    unittest.main()
