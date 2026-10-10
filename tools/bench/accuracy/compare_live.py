#!/usr/bin/env python3
"""Compare Supermetry and Plausible Stats API results for one real site."""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from typing import Any


SUMMARY_METRICS = ["visitors", "visits", "pageviews", "events", "bounce_rate"]


def request_json(url: str, key: str, body: dict[str, Any]) -> dict[str, Any]:
    request = urllib.request.Request(
        url,
        data=json.dumps(body, separators=(",", ":")).encode(),
        method="POST",
        headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        detail = error.read(500).decode(errors="replace")
        raise RuntimeError(f"{url}: HTTP {error.code}: {detail}") from None


def fetch_panel(base: str, key: str, site_id: int, date_from: str, date_to: str) -> dict[str, Any]:
    endpoint = base.rstrip("/") + "/api/public/v1/query"

    def query(dimensions: list[str], metrics: list[str], limit: int = 1000) -> dict[str, Any]:
        return request_json(
            endpoint,
            key,
            {
                "site_id": site_id,
                "date_range": {"from": date_from, "to": date_to},
                "dimensions": dimensions,
                "metrics": metrics,
                "limit": limit,
            },
        )

    summary = query([], SUMMARY_METRICS)
    pages = query(["page"], ["pageviews"], 10)
    sources = query(["source"], ["visitors"], 10)
    return {
        "totals": summary["totals"],
        "pages": [row["page"] for row in pages.get("rows", [])[:10]],
        "sources": [_normal_source(row["source"]) for row in sources.get("rows", [])[:10]],
    }


def fetch_plausible(base: str, key: str, site: str, date_from: str, date_to: str) -> dict[str, Any]:
    endpoint = base.rstrip("/") + "/api/v2/query"

    def query(dimensions: list[str], metrics: list[str], limit: int = 1000) -> dict[str, Any]:
        return request_json(
            endpoint,
            key,
            {
                "site_id": site,
                "date_range": [date_from, date_to],
                "dimensions": dimensions,
                "metrics": metrics,
                "order_by": [[metrics[0], "desc"]],
                "pagination": {"limit": limit, "offset": 0},
            },
        )

    summary_response = query([], SUMMARY_METRICS)
    summary_values = summary_response.get("results", [{}])[0].get("metrics", [])
    totals = dict(zip(SUMMARY_METRICS, summary_values))
    pages = query(["event:page"], ["pageviews"], 10).get("results", [])
    sources = query(["visit:source"], ["visitors"], 10).get("results", [])
    return {
        "totals": totals,
        "pages": [row["dimensions"][0] for row in pages[:10]],
        "sources": [_normal_source(row["dimensions"][0]) for row in sources[:10]],
    }


def compare_snapshots(supermetry: dict[str, Any], plausible: dict[str, Any]) -> list[dict[str, Any]]:
    rows = []
    for metric in SUMMARY_METRICS:
        left = float(supermetry["totals"].get(metric, 0))
        right = float(plausible["totals"].get(metric, 0))
        if metric in {"pageviews", "events"}:
            passed = left == right
            tolerance = "exact"
        elif metric == "bounce_rate":
            passed = abs(left - right) <= 2
            tolerance = "2 pp"
        else:
            denominator = max(abs(right), 1)
            passed = abs(left - right) / denominator <= 0.02
            tolerance = "2%"
        rows.append(_row(metric, left, right, tolerance, passed))
    for metric, key in (("top_pages", "pages"), ("top_sources", "sources")):
        left = set(supermetry[key][:10])
        right = set(plausible[key][:10])
        rows.append(_row(metric, sorted(left), sorted(right), "same set", left == right))
    return rows


def render_delta_table(rows: list[dict[str, Any]]) -> str:
    output = ["| Metric | Supermetry | Plausible | Delta | Tolerance | Result |", "|---|---:|---:|---:|---|---|"]
    for row in rows:
        output.append(
            f"| {row['metric']} | {_display(row['supermetry'])} | {_display(row['plausible'])} | "
            f"{_display(row['delta'])} | {row['tolerance']} | {'PASS' if row['pass'] else 'FAIL'} |"
        )
    return "\n".join(output)


def _row(metric: str, supermetry: Any, plausible: Any, tolerance: str, passed: bool) -> dict[str, Any]:
    delta = supermetry - plausible if isinstance(supermetry, float) else "set"
    return {"metric": metric, "supermetry": supermetry, "plausible": plausible, "delta": delta, "tolerance": tolerance, "pass": passed}


def _display(value: Any) -> str:
    if isinstance(value, float):
        return f"{value:.3f}"
    if isinstance(value, list):
        return ", ".join(value)
    return str(value)


def _normal_source(value: str) -> str:
    return "(direct)" if value in {"Direct / None", "Direct", "(direct)"} else value


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--from", dest="date_from", required=True)
    parser.add_argument("--to", dest="date_to", required=True)
    args = parser.parse_args(argv)
    required = ["SUPERMETRY_URL", "SUPERMETRY_KEY", "SUPERMETRY_SITE_ID", "PLAUSIBLE_URL", "PLAUSIBLE_KEY", "PLAUSIBLE_SITE"]
    missing = [name for name in required if not os.environ.get(name)]
    if missing:
        parser.error("missing environment variables: " + ", ".join(missing))
    supermetry = fetch_panel(
        os.environ["SUPERMETRY_URL"], os.environ["SUPERMETRY_KEY"], int(os.environ["SUPERMETRY_SITE_ID"]), args.date_from, args.date_to
    )
    plausible = fetch_plausible(
        os.environ["PLAUSIBLE_URL"], os.environ["PLAUSIBLE_KEY"], os.environ["PLAUSIBLE_SITE"], args.date_from, args.date_to
    )
    rows = compare_snapshots(supermetry, plausible)
    print(render_delta_table(rows))
    return 0 if all(row["pass"] for row in rows) else 1


if __name__ == "__main__":
    sys.exit(main())
