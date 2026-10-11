import json
import tempfile
import threading
import unittest
import urllib.parse
from pathlib import Path

from ab import volume


def sample_visit(visit_id: str = "v0001") -> dict:
    return {
        "id": visit_id,
        "ip": "198.51.100.17",
        "user_agent": "Mozilla/5.0 Chrome/129.0.0.0 Safari/537.36",
        "viewport": [1366, 768],
        "device": "desktop",
        "browser": "Chrome",
        "os": "Windows",
        "referrer": "https://www.google.com/",
        "utm": {
            "utm_source": "newsletter",
            "utm_medium": "email",
            "utm_campaign": "launch",
        },
        "start_offset_ms": 0,
        "steps": [
            {"kind": "goto", "path": "/pricing", "ms": 0, "name": "", "props": {}, "revenue": None, "currency": "USD", "href": ""},
            {"kind": "wait", "path": "", "ms": 10, "name": "", "props": {}, "revenue": None, "currency": "USD", "href": ""},
            {"kind": "event", "path": "", "ms": 0, "name": "Purchase", "props": {"plan": "pro"}, "revenue": 49.0, "currency": "USD", "href": ""},
            {"kind": "spa", "path": "/spa/b", "ms": 0, "name": "", "props": {}, "revenue": None, "currency": "USD", "href": ""},
            {"kind": "outbound", "path": "", "ms": 0, "name": "", "props": {}, "revenue": None, "currency": "USD", "href": "https://external.test/read"},
            {"kind": "download", "path": "", "ms": 0, "name": "", "props": {}, "revenue": None, "currency": "USD", "href": "/files/report.pdf"},
        ],
    }


class EventMappingTest(unittest.TestCase):
    def test_visit_events_keep_navigation_and_custom_event_order(self) -> None:
        events = volume.visit_events(sample_visit())

        self.assertEqual(
            [event.name for event in events],
            ["pageview", "Purchase", "pageview", "Outbound Link: Click", "File Download"],
        )
        self.assertEqual(events[0].url, "http://app.test/pricing?utm_source=newsletter&utm_medium=email&utm_campaign=launch")
        self.assertEqual(events[1].url, events[0].url)
        self.assertEqual(events[2].url, "http://app.test/spa/b")
        self.assertEqual(events[3].props, {"url": "https://external.test/read"})
        self.assertEqual(events[4].props, {"url": "http://app.test/files/report.pdf"})
        self.assertEqual(events[1].revenue, {"amount": 49.0, "currency": "USD"})

    def test_build_requests_use_identical_identity_and_public_contract(self) -> None:
        event = volume.visit_events(sample_visit())[0]
        stack = {
            "plausible": {"url": "http://127.0.0.1:8000", "domain": "site1.test"},
            "supermetry": {
                "api_url": "http://127.0.0.1:18090",
                "public_id": "abc123def456",
            },
        }

        plausible, supermetry = volume.build_requests(sample_visit(), event, stack)

        self.assertEqual(plausible.url, "http://127.0.0.1:8000/api/event")
        self.assertEqual(supermetry.url, "http://127.0.0.1:18090/api/public/v1/events")
        for request in (plausible, supermetry):
            self.assertEqual(request.headers["User-Agent"], sample_visit()["user_agent"])
            self.assertEqual(request.headers["X-Forwarded-For"], sample_visit()["ip"])
        self.assertEqual(supermetry.headers["Origin"], "http://app.test")
        self.assertEqual(supermetry.headers["Host"], "app.test:18090")
        self.assertEqual(supermetry.body["site"], "abc123def456")
        self.assertEqual(supermetry.body["name"], "pageview")
        self.assertEqual(supermetry.body["url"], event.url)
        self.assertEqual(plausible.body["domain"], "site1.test")


class VolumeRunTest(unittest.TestCase):
    def setUp(self) -> None:
        self.stack = {
            "plausible": {"url": "http://127.0.0.1:8000", "domain": "site1.test"},
            "supermetry": {"api_url": "http://127.0.0.1:18090", "public_id": "abc123def456"},
        }

    def test_send_visit_finishes_each_event_for_both_products_before_next(self) -> None:
        calls: list[tuple[str, str]] = []

        def post(request: volume.Request) -> volume.Response:
            calls.append((request.product, request.body["name"]))
            return volume.Response(status=202, headers={})

        result = volume.send_visit(sample_visit(), self.stack, post=post)

        self.assertEqual(result["sent"], {"plausible": 5, "supermetry": 5})
        self.assertEqual(result["failures"], [])
        self.assertEqual(
            calls,
            [item for name in ["pageview", "Purchase", "pageview", "Outbound Link: Click", "File Download"] for item in [("plausible", name), ("supermetry", name)]],
        )

    def test_dropped_or_non_2xx_responses_are_reported(self) -> None:
        def post(request: volume.Request) -> volume.Response:
            if request.product == "plausible":
                return volume.Response(status=202, headers={"x-plausible-dropped": ""})
            return volume.Response(status=429, headers={})

        result = volume.send_visit(sample_visit(), self.stack, post=post)

        self.assertEqual(result["sent"], {"plausible": 0, "supermetry": 0})
        self.assertEqual(len(result["failures"]), 10)
        self.assertTrue(any("x-plausible-dropped" in failure["error"] for failure in result["failures"]))
        self.assertTrue(any("HTTP 429" in failure["error"] for failure in result["failures"]))

    def test_run_uses_bounded_workers_and_writes_report(self) -> None:
        visits = [sample_visit(f"v{index:04d}") for index in range(1, 6)]
        active = 0
        peak = 0
        lock = threading.Lock()

        def sender(visit: dict, _stack: dict, *, post) -> dict:
            nonlocal active, peak
            with lock:
                active += 1
                peak = max(peak, active)
            threading.Event().wait(0.01)
            with lock:
                active -= 1
            return {"visit_id": visit["id"], "sent": {"plausible": 1, "supermetry": 1}, "failures": []}

        with tempfile.TemporaryDirectory() as directory:
            report_path = Path(directory) / "volume-report.json"
            report = volume.run_volume(
                {"seed": 7, "visits": visits},
                self.stack,
                concurrency=2,
                report_path=report_path,
                post=lambda _request: volume.Response(202, {}),
                sender=sender,
            )

            self.assertLessEqual(peak, 2)
            self.assertEqual(report["sent"], {"plausible": 5, "supermetry": 5})
            self.assertEqual(report["failures"], [])
            self.assertEqual(json.loads(report_path.read_text(encoding="utf-8")), report)


if __name__ == "__main__":
    unittest.main()


class VolumeFidelityTests(unittest.TestCase):
    def test_utm_keys_become_utm_query_parameters(self):
        events = volume.visit_events(
            {
                "referrer": "",
                "utm": {"source": "google", "medium": "cpc", "campaign": "launch"},
                "steps": [{"kind": "goto", "path": "/pricing"}],
            }
        )
        query = urllib.parse.parse_qs(urllib.parse.urlsplit(events[0].url).query)
        self.assertEqual(query, {"utm_source": ["google"], "utm_medium": ["cpc"], "utm_campaign": ["launch"]})

    def test_already_prefixed_utm_keys_are_not_prefixed_twice(self):
        events = volume.visit_events({"referrer": "", "utm": {"utm_campaign": "launch"}, "steps": [{"kind": "goto", "path": "/"}]})
        self.assertEqual(urllib.parse.parse_qs(urllib.parse.urlsplit(events[0].url).query), {"utm_campaign": ["launch"]})

    def test_spa_navigation_to_the_current_page_sends_no_pageview(self):
        events = volume.visit_events(
            {
                "referrer": "",
                "utm": {},
                "steps": [
                    {"kind": "goto", "path": "/spa"},
                    {"kind": "spa", "path": "/spa/a"},
                    {"kind": "spa", "path": "/spa/a"},
                    {"kind": "spa", "path": "/spa/b"},
                ],
            }
        )
        self.assertEqual([urllib.parse.urlsplit(e.url).path for e in events], ["/spa", "/spa/a", "/spa/b"])

    def test_a_repeated_full_page_load_still_sends_a_pageview(self):
        events = volume.visit_events({"referrer": "", "utm": {}, "steps": [{"kind": "goto", "path": "/a"}, {"kind": "goto", "path": "/a"}]})
        self.assertEqual(len(events), 2)
