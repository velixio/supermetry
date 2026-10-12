import tempfile
import unittest
from pathlib import Path

from compare_live import compare_snapshots, load_credentials, render_delta_table, summarize_live


class CompareLiveTest(unittest.TestCase):
    def test_credentials_are_parsed_without_shell_expansion(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "creds.env"
            path.write_text("\n".join(
                f"{product}_{field}=literal-$VALUE"
                for product in ("PLAUSIBLE", "UMAMI", "SUPERMETRY_LIVE")
                for field in ("URL", "USER", "PASS")
            ))
            credentials = load_credentials(path)
        self.assertEqual(credentials["PLAUSIBLE_PASS"], "literal-$VALUE")

    def test_live_summary_quantifies_pairwise_differences_and_skew(self):
        samples = [
            {"sampling_skew_ms": 100, "supermetry": {"online": 12}, "plausible": {"online": 10}, "umami": {"online": 8}},
            {"sampling_skew_ms": 200, "supermetry": {"online": 15}, "plausible": {"online": 12}, "umami": {"online": 10}},
        ]
        summary = summarize_live(samples)
        self.assertEqual(summary["samples"], 2)
        self.assertEqual(summary["pairs"]["supermetry_vs_plausible"]["mean_delta"], 2.5)
        self.assertEqual(summary["sampling_skew_ms"], {"mean": 150.0, "max": 200.0})

    def test_tolerances_distinguish_percent_from_percentage_points(self):
        supermetry = {
            "totals": {"visitors": 100, "visits": 120, "pageviews": 250, "events": 20, "bounce_rate": 55.0},
            "pages": ["/", "/pricing"],
            "sources": ["Direct / None", "Google"],
        }
        plausible = {
            "totals": {"visitors": 102, "visits": 118, "pageviews": 250, "events": 20, "bounce_rate": 56.9},
            "pages": ["/pricing", "/"],
            "sources": ["Google", "Direct / None"],
        }
        rows = compare_snapshots(supermetry, plausible)
        self.assertTrue(all(row["pass"] for row in rows))
        self.assertIn("bounce_rate", render_delta_table(rows))

    def test_exact_and_top_ten_mismatches_fail(self):
        supermetry = {
            "totals": {"visitors": 100, "visits": 100, "pageviews": 9, "events": 2, "bounce_rate": 50},
            "pages": ["/a"],
            "sources": ["Google"],
        }
        plausible = {
            "totals": {"visitors": 100, "visits": 100, "pageviews": 10, "events": 3, "bounce_rate": 50},
            "pages": ["/b"],
            "sources": ["Google"],
        }
        failed = {row["metric"] for row in compare_snapshots(supermetry, plausible) if not row["pass"]}
        self.assertEqual(failed, {"pageviews", "events", "top_pages"})


if __name__ == "__main__":
    unittest.main()
