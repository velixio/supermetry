import ipaddress
import unittest
from urllib.parse import urlsplit

from ab.scenarios import from_json, generate, to_json


ALLOWED_NETWORKS = (
    ipaddress.ip_network("198.51.100.0/24"),
    ipaddress.ip_network("203.0.113.0/24"),
)


class ScenarioTest(unittest.TestCase):
    def test_generate_is_deterministic_and_respects_visitor_count(self):
        first = generate(301, 200)
        second = generate(301, 200)

        self.assertEqual(to_json(first), to_json(second))
        self.assertEqual(len(first.visits), 200)

    def test_json_round_trip_preserves_scenario(self):
        scenario = generate(17, 25)

        self.assertEqual(from_json(to_json(scenario)), scenario)
        self.assertTrue(to_json(scenario).endswith("\n"))

    def test_visitors_have_unique_allowed_identities(self):
        scenario = generate(99, 1_200)
        identities = {(visit.ip, visit.user_agent) for visit in scenario.visits}

        self.assertEqual(len(identities), len(scenario.visits))
        for visit in scenario.visits:
            address = ipaddress.ip_address(visit.ip)
            self.assertTrue(any(address in network for network in ALLOWED_NETWORKS), visit.ip)

    def test_device_user_agent_and_viewport_are_consistent(self):
        scenario = generate(42, 500)

        for visit in scenario.visits:
            width, height = visit.viewport
            self.assertGreater(height, width if visit.device != "desktop" else 0)
            if visit.device == "desktop":
                self.assertGreaterEqual(width, 1_024)
                self.assertNotIn("Mobile", visit.user_agent)
            elif visit.device == "mobile":
                self.assertLessEqual(width, 500)
                self.assertIn("Mobile", visit.user_agent)
            elif visit.device == "tablet":
                self.assertLessEqual(width, 900)
                self.assertIn("Mobile", visit.user_agent)
            else:
                self.fail(f"unknown device {visit.device!r}")

    def test_steps_start_with_goto_and_outbound_links_are_external(self):
        scenario = generate(7, 500)
        outbound_steps = []

        for visit in scenario.visits:
            self.assertTrue(visit.steps)
            self.assertEqual(visit.steps[0].kind, "goto")
            outbound_steps.extend(step for step in visit.steps if step.kind == "outbound")

        self.assertTrue(outbound_steps)
        for step in outbound_steps:
            target = urlsplit(step.href)
            self.assertIn(target.scheme, {"http", "https"})
            self.assertTrue(target.hostname)
            self.assertNotEqual(target.hostname, "app.test")


if __name__ == "__main__":
    unittest.main()


class IpOffsetTests(unittest.TestCase):
    def test_offset_scenarios_never_share_an_ip_and_user_agent_pair(self) -> None:
        first = generate(7, 300)
        second = generate(8, 1200, ip_offset=300)
        pairs = [(v.ip, v.user_agent) for v in (*first.visits, *second.visits)]
        self.assertEqual(len(pairs), len(set(pairs)))
        ids = [v.id for v in (*first.visits, *second.visits)]
        self.assertEqual(len(ids), len(set(ids)))

    def test_zero_offset_matches_the_default(self) -> None:
        self.assertEqual(to_json(generate(7, 50)), to_json(generate(7, 50, ip_offset=0)))

    def test_offset_is_deterministic(self) -> None:
        a = to_json(generate(9, 40, ip_offset=500))
        b = to_json(generate(9, 40, ip_offset=500))
        self.assertEqual(a, b)

    def test_negative_offset_is_rejected(self) -> None:
        with self.assertRaises(ValueError):
            generate(7, 10, ip_offset=-1)
