import json
import tempfile
import unittest
from pathlib import Path

from prepare import prepare_dataset


class PrepareTest(unittest.TestCase):
    def test_filters_events_and_writes_product_compatible_sites(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source_sites = root / "source-sites.json"
            source_events = root / "source.ndjson"
            output_sites = root / "sites.json"
            output_events = root / "events.ndjson"
            source_sites.write_text(
                json.dumps(
                    [
                        {"id": 1, "host": "one.test", "timezone": "UTC", "share": 0.8},
                        {"id": 2, "host": "two.test", "timezone": "Asia/Kolkata", "share": 0.2},
                    ]
                ),
                encoding="utf-8",
            )
            source_events.write_text('{"site":1,"ip":"192.0.2.1","ua":"synthetic"}\n{"site":2}\n', encoding="utf-8")
            count = prepare_dataset(source_sites, source_events, output_sites, output_events, {1})
            self.assertEqual(count, 1)
            self.assertEqual(json.loads(output_events.read_text())["site"], 1)
            self.assertEqual(
                json.loads(output_sites.read_text()),
                [
                    {
                        "id": 1,
                        "org_id": 1,
                        "public_id": "one.test",
                        "host": "one.test",
                        "timezone": "UTC",
                        "retention_days": 365,
                        "allowed_hosts": ["one.test"],
                    }
                ],
            )


if __name__ == "__main__":
    unittest.main()
