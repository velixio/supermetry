import unittest

from check import build_cases


class CheckCasesTest(unittest.TestCase):
    def test_cases_cover_ranges_timezones_filters_breakdowns_and_goals(self):
        cases = build_cases("2026-09-24", "2026-10-07")
        dimensions = {tuple(case.get("dimensions", [])) for case in cases}
        self.assertTrue({("page",), ("source",), ("utm_source",), ("utm_medium",), ("utm_campaign",), ("country",), ("device",), ("browser",), ("os",), ("entry",), ("exit",), ("goal",)} <= dimensions)
        self.assertTrue(any(len(case.get("filters", [])) == 2 for case in cases))
        self.assertTrue(any(case["site_id"] == 3 for case in cases))
        self.assertTrue(any(case["site_id"] == 5 for case in cases))
        self.assertTrue(any(case["date_range"] == {"from": "2026-09-24", "to": "2026-10-07"} for case in cases))
        goal = next(case for case in cases if case.get("dimensions") == ["goal"])
        self.assertEqual(goal["metrics"], ["visitors", "events", "conversions", "conversion_rate"])


if __name__ == "__main__":
    unittest.main()
