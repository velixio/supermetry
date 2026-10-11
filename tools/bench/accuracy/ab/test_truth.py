import unittest

from ab.scenarios import Scenario, Step, Visit
from ab.truth import truth


def visit(
    identifier: str,
    *steps: Step,
    ip: str = "198.51.100.1",
    user_agent: str = "test-agent-1",
    referrer: str = "",
    utm: dict[str, str] | None = None,
) -> Visit:
    return Visit(
        id=identifier,
        ip=ip,
        user_agent=user_agent,
        viewport=(1_440, 900),
        device="desktop",
        browser="Chrome",
        os="Windows",
        referrer=referrer,
        utm=utm or {},
        steps=tuple(steps),
        start_offset_ms=0,
    )


def scenario(*visits: Visit) -> Scenario:
    return Scenario(seed=1, visits=tuple(visits))


class TruthTest(unittest.TestCase):
    def test_one_page_visit_is_a_bounce(self):
        result = truth(scenario(visit("one", Step("goto", path="/"))))

        self.assertEqual(result["visitors"], 1)
        self.assertEqual(result["visits"], 1)
        self.assertEqual(result["pageviews"], 1)
        self.assertEqual(result["events"], 1)
        self.assertEqual(result["bounce_rate_plausible"], 100.0)
        self.assertEqual(result["bounce_rate_supermetry"], 100.0)
        self.assertEqual(result["top_sources"], [("(direct)", 1)])

    def test_three_page_journey_is_not_a_bounce(self):
        result = truth(
            scenario(
                visit(
                    "journey",
                    Step("goto", path="/"),
                    Step("goto", path="/pricing"),
                    Step("goto", path="/about"),
                )
            )
        )

        self.assertEqual(result["pageviews"], 3)
        self.assertEqual(result["bounce_rate_plausible"], 0.0)
        self.assertEqual(result["bounce_rate_supermetry"], 0.0)

    def test_custom_event_makes_single_page_visit_non_bounce(self):
        result = truth(
            scenario(
                visit(
                    "event",
                    Step("goto", path="/pricing"),
                    Step("event", name="Signup", props={"plan": "starter"}),
                )
            )
        )

        self.assertEqual(result["custom_events"], {"Signup": 1})
        self.assertEqual(result["bounce_rate_plausible"], 0.0)
        self.assertEqual(result["bounce_rate_supermetry"], 0.0)

    def test_revenue_is_summed_by_currency(self):
        result = truth(
            scenario(
                visit(
                    "buyer",
                    Step("goto", path="/pricing"),
                    Step("event", name="Purchase", revenue=10.0, currency="USD"),
                    Step("event", name="Purchase", revenue=5.5, currency="USD"),
                )
            )
        )

        self.assertEqual(result["revenue"], {"USD": 15.5})

    def test_utm_campaign_is_counted_once_per_visitor(self):
        result = truth(
            scenario(
                visit(
                    "campaign-visitor",
                    Step("goto", path="/?utm_campaign=launch"),
                    Step("spa", path="/spa/a"),
                    utm={"source": "google", "medium": "cpc", "campaign": "launch"},
                )
            )
        )

        self.assertEqual(result["utm_campaign"], {"launch": 1})
        self.assertEqual(result["top_sources"], [("Google", 1)])

    def test_duplicate_ip_and_user_agent_is_rejected(self):
        duplicate = {
            "ip": "203.0.113.9",
            "user_agent": "shared-agent",
        }
        with self.assertRaisesRegex(ValueError, "ip.*user.agent|identity"):
            truth(
                scenario(
                    visit("first", Step("goto", path="/"), **duplicate),
                    visit("second", Step("goto", path="/about"), **duplicate),
                )
            )

    def test_top_pages_sort_by_count_then_path_and_limit_to_ten(self):
        paths = [f"/page-{index:02d}" for index in range(12)]
        visits = []
        for index, path in enumerate(reversed(paths), 1):
            steps = [Step("goto", path=path)]
            if path in {"/page-02", "/page-03"}:
                steps.append(Step("goto", path=path))
            visits.append(
                visit(
                    f"visitor-{index}",
                    *steps,
                    ip=f"198.51.100.{index}",
                    user_agent=f"agent-{index}",
                )
            )

        result = truth(scenario(*visits))

        self.assertEqual(result["top_pages"][:2], [("/page-02", 2), ("/page-03", 2)])
        self.assertEqual(len(result["top_pages"]), 10)
        tied_paths = [path for path in paths if path not in {"/page-02", "/page-03"}]
        self.assertEqual(result["top_pages"][2:], [(path, 1) for path in tied_paths[:8]])

    def test_events_include_pageviews_custom_outbound_and_download(self):
        result = truth(
            scenario(
                visit(
                    "mixed",
                    Step("goto", path="/"),
                    Step("spa", path="/spa/a"),
                    Step("hash", path="/spa/a#details"),
                    Step("event", name="Signup"),
                    Step("outbound", href="https://example.com/out"),
                    Step("download", href="/files/report.pdf"),
                )
            )
        )

        self.assertEqual(result["pageviews"], 3)
        self.assertEqual(result["events"], 6)
        self.assertEqual(
            result["custom_events"],
            {"File Download": 1, "Outbound Link: Click": 1, "Signup": 1},
        )


if __name__ == "__main__":
    unittest.main()


class DuplicateNavigationTests(unittest.TestCase):
    def test_spa_navigation_to_the_current_page_is_not_a_pageview(self):
        result = truth(
            scenario(
                visit(
                    "a",
                    Step("goto", path="/spa"),
                    Step("spa", path="/spa/a"),
                    Step("spa", path="/spa/a"),
                    Step("spa", path="/spa/b"),
                )
            )
        )
        self.assertEqual(result["pageviews"], 3)
        self.assertEqual(result["events"], 3)
        self.assertEqual(dict(result["top_pages"]), {"/spa": 1, "/spa/a": 1, "/spa/b": 1})

    def test_visit_with_only_a_repeated_page_is_still_a_bounce(self):
        result = truth(scenario(visit("a", Step("goto", path="/spa"), Step("spa", path="/spa"))))
        self.assertEqual(result["pageviews"], 1)
        self.assertEqual(result["bounce_rate_plausible"], 100.0)

    def test_a_full_reload_of_the_same_page_is_a_new_pageview(self):
        result = truth(scenario(visit("a", Step("goto", path="/about"), Step("goto", path="/about"))))
        self.assertEqual(result["pageviews"], 2)


class EventsDefinitionTests(unittest.TestCase):
    def test_each_product_has_its_own_events_definition(self):
        result = truth(
            scenario(
                visit(
                    "a",
                    Step("goto", path="/"),
                    Step("goto", path="/pricing"),
                    Step("event", name="Signup"),
                    Step("download", href="https://example.test/f.pdf"),
                )
            )
        )
        self.assertEqual(result["events_plausible"], 4)  # 2 pageviews + 2 custom
        self.assertEqual(result["events_supermetry"], 2)  # custom events only
        self.assertEqual(result["events"], result["events_plausible"])
