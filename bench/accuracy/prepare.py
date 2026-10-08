#!/usr/bin/env python3
"""Select benchmark sites and convert generator metadata for Panel."""

import argparse
import json
from pathlib import Path


def prepare_dataset(
    source_sites: Path,
    source_events: Path,
    output_sites: Path,
    output_events: Path,
    site_ids: set[int],
) -> int:
    sites = json.loads(source_sites.read_text(encoding="utf-8"))
    selected = []
    for site in sites:
        if int(site["id"]) not in site_ids:
            continue
        selected.append(
            {
                "id": site["id"],
                "org_id": 1,
                "public_id": site["host"],
                "host": site["host"],
                "timezone": site["timezone"],
                "retention_days": 365,
                "allowed_hosts": [site["host"]],
            }
        )
    if {int(site["id"]) for site in selected} != site_ids:
        raise ValueError("one or more selected sites are absent from generator metadata")
    output_sites.write_text(json.dumps(selected, separators=(",", ":")) + "\n", encoding="utf-8")
    count = 0
    with source_events.open(encoding="utf-8") as source, output_events.open("w", encoding="utf-8") as output:
        for line in source:
            record = json.loads(line)
            if int(record["site"]) in site_ids:
                output.write(line)
                count += 1
    return count


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-sites", type=Path, required=True)
    parser.add_argument("--source-events", type=Path, required=True)
    parser.add_argument("--sites", type=Path, required=True)
    parser.add_argument("--events", type=Path, required=True)
    parser.add_argument("--site-ids", default="1,3,5")
    args = parser.parse_args()
    site_ids = {int(value) for value in args.site_ids.split(",")}
    count = prepare_dataset(args.source_sites, args.source_events, args.sites, args.events, site_ids)
    print(f"selected {count} events across {len(site_ids)} sites")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
