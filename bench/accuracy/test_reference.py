import json
import tempfile
import unittest
from pathlib import Path

from reference import Reference, compare_response


class ReferenceTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        root = Path(self.temp.name)
        self.sites = root / "sites.json"
        self.events = root / "events.ndjson"
        self.sites.write_text(
            json.dumps([{"id": 1, "host": "site1.test", "timezone": "UTC"}]),
            encoding="utf-8",
        )
        rows = [
            event("2026-08-31T10:00:00Z", "v1", "s1", "/", ref="https://www.google.com/", country="US"),
            event("2026-08-31T10:00:10Z", "v1", "s1", "/", name="signup", props={"plan": "pro"}, ref="https://www.google.com/", country="US"),
            event("2026-08-31T10:00:20Z", "v1", "s1", "/pricing", ref="https://site1.test/", country="US"),
            event("2026-08-31T12:00:00Z", "v1", "s2", "/docs", country="US"),
            event("2026-08-31T13:00:00Z", "v2", "s3", "/", country="IN"),
            event("2026-08-31T13:00:10Z", "v2", "s3", "/", name="signup", props={"plan": "free"}, country="IN"),
            event("2026-09-01T09:00:00Z", "v1", "s4", "/", country="US"),
        ]
        self.events.write_text("".join(json.dumps(row) + "\n" for row in rows), encoding="utf-8")
        self.reference = Reference.from_files(self.events, self.sites)

    def tearDown(self):
        self.temp.cleanup()

    def test_summary_uses_daily_visitors_and_documented_formulas(self):
        result = self.reference.query(1, "2026-08-31", "2026-09-01")
        self.assertEqual(
            result["totals"],
            {
                "visitors": 3.0,
                "visits": 4.0,
                "pageviews": 5.0,
                "views_per_visit": 1.25,
                "bounce_rate": 50.0,
                "visit_duration": 7.5,
            },
        )

    def test_session_filter_and_page_breakdown_use_observable_query_semantics(self):
        filtered = self.reference.query(
            1,
            "2026-08-31",
            "2026-08-31",
            filters=[{"dimension": "country", "operator": "is", "values": ["US"]}],
        )
        self.assertEqual(filtered["totals"]["visitors"], 1.0)
        self.assertEqual(filtered["totals"]["visits"], 2.0)

        page = self.reference.query(1, "2026-08-31", "2026-08-31", dimensions=["page"])
        by_page = {row["page"]: row for row in page["rows"]}
        self.assertEqual(by_page["/"]["visitors"], 2.0)
        self.assertEqual(by_page["/"]["pageviews"], 2.0)
        self.assertEqual(by_page["/pricing"]["visits"], 1.0)

        source = self.reference.query(1, "2026-08-31", "2026-08-31", dimensions=["source"])
        self.assertIn("Google", {row["source"] for row in source["rows"]})
        medium = self.reference.query(1, "2026-08-31", "2026-08-31", dimensions=["utm_medium"])
        self.assertIn("organic", {row["utm_medium"] for row in medium["rows"]})
        utm_source = self.reference.query(1, "2026-08-31", "2026-08-31", dimensions=["utm_source"])
        self.assertEqual(utm_source["rows"], [])

    def test_goal_breakdown_counts_custom_events_and_converted_visitors(self):
        result = self.reference.query(
            1,
            "2026-08-31",
            "2026-08-31",
            dimensions=["goal"],
            metrics=["visitors", "visits", "events", "conversions", "conversion_rate"],
        )
        self.assertEqual(
            result["rows"],
            [{"goal": "signup", "visitors": 2.0, "visits": 2.0, "events": 2.0, "conversions": 2.0, "conversion_rate": 100.0}],
        )
        self.assertEqual(
            result["totals"],
            {"visitors": 2.0, "visits": 3.0, "events": 2.0, "conversions": 0.0, "conversion_rate": 0.0},
        )

    def test_compare_response_reports_each_metric_mismatch(self):
        expected = {"totals": {"visitors": 2.0}, "rows": []}
        actual = {"totals": {"visitors": 3.0}, "rows": []}
        mismatches = compare_response("hot", {"site_id": 1}, expected, actual)
        self.assertEqual(len(mismatches), 1)
        self.assertEqual(mismatches[0]["metric"], "totals.visitors")

    def test_compare_response_ignores_redundant_aggregate_row_without_dimensions(self):
        expected = {"totals": {"visitors": 2.0}, "rows": []}
        actual = {"totals": {"visitors": 2.0}, "rows": [{"visitors": 2.0}]}
        self.assertEqual(compare_response("hot", {"site_id": 1}, expected, actual), [])


def event(ts, visitor, session, path, *, name="pageview", props=None, ref="", country="US"):
    return {
        "ts": ts,
        "site": 1,
        "host": "site1.test",
        "path": path,
        "ref": ref,
        "utm_source": "",
        "utm_medium": "",
        "utm_campaign": "",
        "name": name,
        "props": props or {},
        "ip": "192.0.2.1",
        "ua": "synthetic-test-agent",
        "screen_w": 1440,
        "country": country,
        "browser": "Chrome",
        "os": "Linux",
        "device": "desktop",
        "vk": visitor,
        "sk": session,
    }


if __name__ == "__main__":
    unittest.main()
