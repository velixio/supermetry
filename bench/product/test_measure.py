import unittest
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import measure


class MeasureTests(unittest.TestCase):
    def test_default_dashboard_has_required_requests(self):
        requests = measure.dashboard_requests(30, realtime=True)
        self.assertEqual(len(requests), 16)
        dimensions = [body.get("dimensions", [None])[0] for _, body in requests if body]
        for dimension, _ in measure.BREAKDOWNS:
            self.assertIn(dimension, dimensions)

    def test_percentile_uses_nearest_rank(self):
        self.assertEqual(measure.percentile(list(range(1, 31)), 0.95), 29)


if __name__ == "__main__":
    unittest.main()
