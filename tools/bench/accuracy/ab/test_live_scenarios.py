import unittest
from datetime import datetime, timedelta, timezone

from .live_scenarios import active_visitors, legacy_session_visitors, scenario, truth_table


class LiveScenariosTest(unittest.TestCase):
    def test_exact_edges_repeats_tabs_filters_and_shared_network(self):
        anchor = datetime(2026, 10, 12, 12, 0, 1, tzinfo=timezone.utc)
        events = scenario(anchor)

        self.assertEqual(active_visitors(events, anchor), 5)
        self.assertEqual(active_visitors(events, anchor + timedelta(seconds=1)), 4)
        self.assertEqual(active_visitors(events, anchor + timedelta(seconds=61)), 2)
        self.assertEqual([row["expected"] for row in truth_table(events, anchor)], [5, 4, 2])
        self.assertEqual([legacy_session_visitors(events, anchor + offset) for offset in (timedelta(), timedelta(seconds=1), timedelta(seconds=61))], [6, 5, 3])


if __name__ == "__main__":
    unittest.main()
