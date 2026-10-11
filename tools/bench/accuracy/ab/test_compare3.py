import json
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from ab.compare3 import (
    BROKEN_MESSAGE,
    compare,
    fetch_plausible,
    fetch_supermetry,
    load_known_differences,
    render_json,
    render_markdown,
)


METRICS = (
    "visitors",
    "visits",
    "pageviews",
    "events",
    "top_pages",
    "top_sources",
    "utm_campaign",
    "devices",
    "browsers",
    "oses",
    "custom_events",
    "revenue",
)


def truth_metrics():
    return {
        "visitors": 100,
        "visits": 100,
        "pageviews": 200,
        "events": 220,
        "bounce_rate_supermetry": 40.0,
        "bounce_rate_plausible": 40.0,
        "top_pages": [["/", 120], ["/pricing", 80]],
        "top_sources": [["(direct)", 60], ["Google", 40]],
        "utm_campaign": {"launch": 25},
        "devices": {"desktop": 60, "mobile": 35, "tablet": 5},
        "browsers": {"Chrome": 70, "Safari": 30},
        "oses": {"Windows": 60, "iOS": 40},
        "custom_events": {"Purchase": 20},
        "revenue": {"USD": 199.8},
    }


def product_metrics():
    values = truth_metrics()
    values["bounce_rate"] = values.pop("bounce_rate_supermetry")
    values.pop("bounce_rate_plausible")
    return values


class CompareTest(unittest.TestCase):
    def test_matching_products_match_truth(self):
        rows = compare(truth_metrics(), product_metrics(), product_metrics(), [])

        self.assertEqual([row["metric"] for row in rows], [*METRICS[:4], "bounce_rate", *METRICS[4:]])
        self.assertTrue(all(row["status"] == "MATCH" for row in rows))

    def test_percent_tolerance_includes_two_percent_but_not_more(self):
        at_limit = product_metrics()
        at_limit["visitors"] = 102
        over_limit = product_metrics()
        over_limit["visitors"] = 102.01

        self.assertEqual(self._row(compare(truth_metrics(), at_limit, product_metrics(), []), "visitors")["status"], "MATCH")
        self.assertEqual(
            self._row(compare(truth_metrics(), over_limit, product_metrics(), []), "visitors")["status"],
            "UNEXPLAINED",
        )

    def test_bounce_tolerance_is_two_percentage_points(self):
        at_limit = product_metrics()
        at_limit["bounce_rate"] = 42
        over_limit = product_metrics()
        over_limit["bounce_rate"] = 42.01

        self.assertEqual(self._row(compare(truth_metrics(), at_limit, product_metrics(), []), "bounce_rate")["status"], "MATCH")
        self.assertEqual(
            self._row(compare(truth_metrics(), over_limit, product_metrics(), []), "bounce_rate")["status"],
            "UNEXPLAINED",
        )

    def test_exact_metric_mismatch_is_unexplained(self):
        actual = product_metrics()
        actual["pageviews"] = 199

        row = self._row(compare(truth_metrics(), actual, product_metrics(), []), "pageviews")

        self.assertEqual(row["status"], "UNEXPLAINED")
        self.assertEqual(row["verdict_supermetry"], "mismatch")

    def test_known_difference_explains_only_the_named_product(self):
        actual = product_metrics()
        actual["pageviews"] = 199
        known = [{"metric": "pageviews", "product": "supermetry", "reason": "intentional sample", "evidence": "issue-1"}]

        row = self._row(compare(truth_metrics(), actual, product_metrics(), known), "pageviews")

        self.assertEqual(row["status"], "EXPLAINED")
        self.assertEqual(row["verdict_supermetry"], "intentional sample")

    def test_missing_metric_is_unexplained_and_names_cause(self):
        actual = product_metrics()
        del actual["browsers"]

        row = self._row(compare(truth_metrics(), actual, product_metrics(), []), "browsers")

        self.assertEqual(row["status"], "UNEXPLAINED")
        self.assertEqual(row["verdict_supermetry"], "not available")

    def test_low_pageview_count_marks_every_row_broken(self):
        actual = product_metrics()
        actual["pageviews"] = 99

        rows = compare(truth_metrics(), actual, product_metrics(), [])

        self.assertTrue(all(row["status"] == "BROKEN" for row in rows))
        self.assertTrue(all(row["verdict_supermetry"] == BROKEN_MESSAGE for row in rows))

    def test_top_lists_compare_as_sets(self):
        actual = product_metrics()
        actual["top_pages"] = list(reversed(actual["top_pages"]))

        row = self._row(compare(truth_metrics(), actual, product_metrics(), []), "top_pages")

        self.assertEqual(row["status"], "MATCH")

    def test_known_differences_loader_reads_a_json_array(self):
        entries = [{"metric": "events", "product": "plausible", "reason": "documented", "evidence": "issue-1"}]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "known.json"
            path.write_text(json.dumps(entries), encoding="utf-8")

            self.assertEqual(load_known_differences(path), entries)

    def test_markdown_and_json_rendering_are_stable(self):
        rows = [
            {
                "metric": "visitors",
                "truth": 10,
                "supermetry": 10,
                "plausible": 11,
                "verdict_supermetry": "MATCH",
                "verdict_plausible": "mismatch",
                "status": "UNEXPLAINED",
            }
        ]
        meta = {"date_from": "2026-10-10", "date_to": "2026-10-11", "stability": "NOT STABLE"}

        self.assertEqual(
            render_markdown(rows, meta),
            "# Accuracy A/B report\n\n"
            "- Date range: 2026-10-10 to 2026-10-11 (UTC)\n"
            "- Ingestion: NOT STABLE\n\n"
            "| Metric | Truth | Supermetry | Plausible | Supermetry verdict | Plausible verdict | Status |\n"
            "|---|---:|---:|---:|---|---|---|\n"
            "| visitors | 10 | 10 | 11 | MATCH | mismatch | UNEXPLAINED |\n",
        )
        self.assertEqual(json.loads(render_json(rows, meta)), {"meta": meta, "rows": rows})

    @staticmethod
    def _row(rows, metric):
        return next(row for row in rows if row["metric"] == metric)


class FetchTest(unittest.TestCase):
    def test_fetch_supermetry_maps_query_rows_to_truth_contract(self):
        def response(_url, _key, body):
            dimension = (body.get("dimensions") or [""])[0]
            if not dimension:
                return {"totals": {"visitors": 3, "visits": 3, "pageviews": 4, "events": 5, "bounce_rate": 50}}
            fixtures = {
                "page": [{"page": "/", "pageviews": 4}],
                "source": [{"source": "Direct / None", "visitors": 3}],
                "utm_campaign": [{"utm_campaign": "launch", "visitors": 2}],
                "device": [{"device": "Desktop", "visitors": 3}],
                "browser": [{"browser": "Chrome", "visitors": 3}],
                "os": [{"os": "Windows", "visitors": 3}],
                "event": [{"event": "Purchase", "events": 1, "total_revenue": 49}],
            }
            totals = {"total_revenue": 49} if dimension == "event" else {}
            return {"rows": fixtures[dimension], "totals": totals}

        stack = {"supermetry": {"api_url": "http://127.0.0.1:18090", "key": "key", "site_id": 1}}
        with mock.patch("ab.compare3.request_json", side_effect=response):
            result = fetch_supermetry(stack, "2026-10-10", "2026-10-11")

        self.assertEqual(result["top_pages"], [["/", 4]])
        self.assertEqual(result["top_sources"], [["(direct)", 3]])
        self.assertEqual(result["devices"], {"desktop": 3})
        self.assertEqual(result["custom_events"], {"Purchase": 1})
        self.assertEqual(result["revenue"], {"USD": 49})

    def test_fetch_plausible_uses_event_name_without_registered_goals(self):
        def response(_url, _key, body):
            dimension = (body.get("dimensions") or [""])[0]
            if not dimension:
                return {"results": [{"metrics": [3, 3, 4, 5, 50]}]}
            fixtures = {
                "event:page": [{"dimensions": ["/"], "metrics": [4]}],
                "visit:source": [{"dimensions": ["Direct"], "metrics": [3]}],
                "visit:utm_campaign": [{"dimensions": ["launch"], "metrics": [2]}],
                "visit:device": [{"dimensions": ["Desktop"], "metrics": [3]}],
                "visit:browser": [{"dimensions": ["Chrome"], "metrics": [3]}],
                "visit:os": [{"dimensions": ["Windows"], "metrics": [3]}],
                "event:name": [{"dimensions": ["Purchase"], "metrics": [1, {"value": 49, "currency": "USD"}]}],
            }
            return {"results": fixtures[dimension]}

        stack = {"plausible": {"url": "http://127.0.0.1:8000", "key": "key", "domain": "site1.test"}}
        with mock.patch("ab.compare3.request_json", side_effect=response):
            result = fetch_plausible(stack, "2026-10-10", "2026-10-11")

        self.assertEqual(result["custom_events"], {"Purchase": 1})
        self.assertEqual(result["revenue"], {"USD": 49})


if __name__ == "__main__":
    unittest.main()
