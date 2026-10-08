#!/usr/bin/env python3
"""Measure Panel's HTTP query set and compare the 30-day totals with truth."""

import argparse
import concurrent.futures
import json
import math
import time
import urllib.error
import urllib.request
from pathlib import Path


BREAKDOWNS = [
    ("page", ["visitors", "pageviews", "time_on_page", "exit_rate"]),
    ("entry", ["visitors"]),
    ("exit", ["visitors"]),
    ("source", ["visitors"]),
    ("utm_source", ["visitors"]),
    ("utm_medium", ["visitors"]),
    ("utm_campaign", ["visitors"]),
    ("country", ["visitors"]),
    ("browser", ["visitors"]),
    ("os", ["visitors"]),
    ("device", ["visitors"]),
    ("goal", ["conversions"]),
    ("event", ["events"]),
]


def percentile(samples, fraction):
    ordered = sorted(samples)
    return ordered[math.ceil(len(ordered) * fraction) - 1]


class Client:
    def __init__(self, base, key):
        self.base = base
        self.headers = {"Authorization": f"Bearer {key}", "Content-Type": "application/json"}

    def request(self, path, body=None):
        data = None if body is None else json.dumps(body, separators=(",", ":")).encode()
        request = urllib.request.Request(self.base + path, data=data, headers=self.headers, method="GET" if body is None else "POST")
        started = time.perf_counter()
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                payload = response.read()
        except urllib.error.HTTPError as error:
            raise RuntimeError(f"HTTP {error.code} for {path}: {error.read(300)!r}") from None
        return (time.perf_counter() - started) * 1000, json.loads(payload)


def query(days, filters=None, dimensions=None, metrics=None, limit=None):
    body = {
        "site_id": 1,
        "date_range": {"from": "2026-09-01" if days == 30 else "2026-07-03", "to": "2026-09-30"},
        "metrics": metrics or ["visitors", "visits", "pageviews", "bounce_rate", "visit_duration"],
        "filters": filters or [],
    }
    if dimensions is not None:
        body["dimensions"] = dimensions
    if limit is not None:
        body["limit"] = limit
    return "/api/public/v1/query", body


def filt(dimension, value):
    return {"dimension": dimension, "operator": "is", "values": [value]}


def dashboard_requests(days, filters=None, realtime=False):
    requests = [
        query(days, filters),
        query(days, filters, ["time"], ["visitors", "visits", "pageviews"]),
    ]
    requests.extend(query(days, filters, [dimension], metrics, 10) for dimension, metrics in BREAKDOWNS)
    if realtime:
        requests.append(("/api/public/v1/realtime?site_id=1", None))
    return requests


def measure_case(client, requests):
    samples = []
    for iteration in range(33):
        started = time.perf_counter()
        with concurrent.futures.ThreadPoolExecutor(max_workers=len(requests)) as executor:
            futures = [executor.submit(client.request, path, body) for path, body in requests]
            responses = [future.result() for future in futures]
        elapsed = (time.perf_counter() - started) * 1000
        if iteration >= 3:
            samples.append(elapsed)
        # The public API allows two requests/s with burst 30. Refill the
        # dashboard's token cost outside the measured interval.
        time.sleep(max(0, len(requests) / 2 + 0.1 - elapsed / 1000))
    return {
        "p50_ms": percentile(samples, 0.50),
        "p95_ms": percentile(samples, 0.95),
        "max_ms": max(samples),
        "requests": len(requests),
    }


def measure_single(client, path, body):
    samples = []
    for iteration in range(33):
        elapsed, _ = client.request(path, body)
        if iteration >= 3:
            samples.append(elapsed)
        time.sleep(0.51)
    return {"p50_ms": percentile(samples, 0.50), "p95_ms": percentile(samples, 0.95), "max_ms": max(samples)}


def correctness(client, truth_path):
    truth = json.loads(Path(truth_path).read_text(encoding="utf-8"))
    expected = {"visitors": 0, "visits": 0, "pageviews": 0, "bounces": 0, "duration_sum_s": 0.0}
    for row in truth["rows"]:
        if row["site"] == 1 and "2026-09-01" <= row["date"] <= "2026-09-30":
            for key in expected:
                expected[key] += row[key]
    time.sleep(0.51)
    _, response = client.request(*query(30))
    actual = response["totals"]
    checks = {
        "visitors": round(actual["visitors"]) == expected["visitors"],
        "visits": round(actual["visits"]) == expected["visits"],
        "pageviews": round(actual["pageviews"]) == expected["pageviews"],
        "bounces": round(actual["bounce_rate"] * expected["visits"] / 100) == expected["bounces"],
        "duration": abs(actual["visit_duration"] * expected["visits"] - expected["duration_sum_s"]) <= max(expected["duration_sum_s"] * 0.01, 0.001),
    }
    return {"mismatches": sum(not value for value in checks.values()), "checks": checks, "expected": expected, "actual": actual}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default="http://127.0.0.1:18080")
    parser.add_argument("--key", required=True)
    parser.add_argument("--truth", required=True)
    parser.add_argument("--out", required=True)
    args = parser.parse_args()
    client = Client(args.base, args.key)
    cases = {}
    for days in (30, 90):
        cases[f"DASHBOARD_S{days}"] = measure_case(client, dashboard_requests(days, realtime=True))
        cases[f"S2_S{days}"] = cases[f"DASHBOARD_S{days}"]
        for name, filters in (
            ("country", [filt("country", "US")]),
            ("country_source", [filt("country", "US"), filt("source", "(direct)")]),
            ("country_path", [filt("country", "US"), filt("path", "/")]),
        ):
            cases[f"S3_S{days}_{name}"] = measure_case(client, dashboard_requests(days, filters))
    cases["S4_S30"] = measure_single(client, "/api/public/v1/funnels/query", {"site_id": 1, "date_range": {"from": "2026-09-01", "to": "2026-09-30"}, "steps": [{"type": "page", "match": "exact", "value": "/"}, {"type": "page", "match": "exact", "value": "/pricing"}, {"type": "event", "match": "exact", "value": "signup"}]})
    cases["F4_S30"] = measure_single(client, *query(30, [filt("event", "signup"), filt("country", "US")], ["prop:plan"], ["events"], 10))
    output = {"timings": cases, "c1": correctness(client, args.truth)}
    Path(args.out).write_text(json.dumps(output, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
