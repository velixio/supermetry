#!/usr/bin/env python3
"""Query Panel and compare every response with the independent reference."""

from __future__ import annotations

import argparse
import json
import urllib.error
import urllib.request
from datetime import date, timedelta
from pathlib import Path
from typing import Any

from reference import DEFAULT_METRICS, Reference, compare_response


def build_cases(date_from: str, date_to: str) -> list[dict[str, Any]]:
    start = date.fromisoformat(date_from)
    end = date.fromisoformat(date_to)
    single = min(start + timedelta(days=6), end)
    seven_from = min(start + timedelta(days=4), end)
    seven_to = min(seven_from + timedelta(days=6), end)

    def query(site: int, begin: date, finish: date, **extra: Any) -> dict[str, Any]:
        return {
            "site_id": site,
            "date_range": {"from": begin.isoformat(), "to": finish.isoformat()},
            "metrics": list(extra.pop("metrics", DEFAULT_METRICS)),
            "limit": 1000,
        } | extra

    cases = [
        query(1, single, single),
        query(1, seven_from, seven_to),
        query(1, start, end),
        query(1, start, end, filters=[{"dimension": "country", "operator": "is", "values": ["US"]}]),
        query(
            1,
            start,
            end,
            filters=[
                {"dimension": "country", "operator": "is", "values": ["US"]},
                {"dimension": "source", "operator": "is", "values": ["Google"]},
            ],
        ),
    ]
    for dimension in (
        "page",
        "source",
        "utm_source",
        "utm_medium",
        "utm_campaign",
        "country",
        "device",
        "browser",
        "os",
        "entry",
        "exit",
    ):
        cases.append(query(1, start, end, dimensions=[dimension]))
    cases.append(
        query(
            1,
            start,
            end,
            dimensions=["goal"],
            metrics=["visitors", "events", "conversions", "conversion_rate"],
        )
    )
    cases.extend(
        [
            query(3, single, single),
            query(3, start, end),
            query(3, start, end, dimensions=["country"]),
            query(5, single, single),
            query(5, start, end),
        ]
    )
    return cases


def query_panel(base_url: str, key: str, body: dict[str, Any]) -> dict[str, Any]:
    request = urllib.request.Request(
        base_url.rstrip("/") + "/api/public/v1/query",
        data=json.dumps(body, separators=(",", ":")).encode(),
        method="POST",
        headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        detail = error.read(500).decode(errors="replace")
        raise RuntimeError(f"query returned HTTP {error.code}: {detail}") from None


def run(
    events: Path,
    sites: Path,
    base_url: str,
    key: str,
    phase: str,
    date_from: str,
    date_to: str,
) -> dict[str, Any]:
    reference = Reference.from_files(events, sites)
    mismatches = []
    sources = set()
    cases = build_cases(date_from, date_to)
    for query in cases:
        expected = reference.query(
            query["site_id"],
            query["date_range"]["from"],
            query["date_range"]["to"],
            dimensions=query.get("dimensions"),
            metrics=query.get("metrics"),
            filters=query.get("filters"),
            limit=query["limit"],
        )
        actual = query_panel(base_url, key, query)
        mismatches.extend(compare_response(phase, query, expected, actual))
        sources.update(actual.get("meta", {}).get("sources_used", []))
    return {"phase": phase, "queries": len(cases), "sources_used": sorted(sources), "mismatches": mismatches}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--events", type=Path, required=True)
    parser.add_argument("--sites", type=Path, required=True)
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--key", required=True)
    parser.add_argument("--phase", choices=("hot", "closed"), required=True)
    parser.add_argument("--from", dest="date_from", required=True)
    parser.add_argument("--to", dest="date_to", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    result = run(args.events, args.sites, args.base_url, args.key, args.phase, args.date_from, args.date_to)
    args.output.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({key: value for key, value in result.items() if key != "mismatches"} | {"mismatch_count": len(result["mismatches"])}))
    return 0 if not result["mismatches"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
