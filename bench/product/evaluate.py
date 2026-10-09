#!/usr/bin/env python3
"""Evaluate and render the product benchmark's machine-readable measurements."""

import argparse
import json
from pathlib import Path


def check(metric_id, passed, value, threshold, method):
    return {"id": metric_id, "pass": bool(passed), "value": value, "threshold": threshold, "method": method}


def build_checks(measured, thresholds):
    s1 = measured["S1"]
    checks = [
        check("STARTUP", measured["startup_ms"] < thresholds["startup_ms"], f'{measured["startup_ms"]:.1f} ms', f'< {thresholds["startup_ms"]} ms', "process start to first successful /readyz response"),
        check("S1", s1["ok_rate"] >= thresholds["ingest_rate"] and s1["p99_ms"] < thresholds["ingest_p99_ms"] and s1["client_overflow"] == 0 and s1["ok_2xx"] == s1["sent"], f'{s1["ok_rate"]:.0f} events/s, p99 {s1["p99_ms"]:.1f} ms, {s1["sent"]-s1["ok_2xx"]+s1["client_overflow"]} drops', f'>= {thresholds["ingest_rate"]} events/s, p99 < {thresholds["ingest_p99_ms"]} ms, 0 drops', "60-second open-loop HTTP load test"),
        check("DASHBOARD", max(measured["dashboard_p95_ms"].values()) < thresholds["dashboard_p95_ms"], f'S30 {measured["dashboard_p95_ms"]["S30"]:.1f} ms; S90 {measured["dashboard_p95_ms"]["S90"]:.1f} ms', f'< {thresholds["dashboard_p95_ms"]} ms', "p95 wall time of summary, timeseries, 13 breakdowns, and realtime fired concurrently; 3 warmups + 30 runs"),
        check("S2", max(measured["S2_p95_ms"].values()) < 300, f'S30 {measured["S2_p95_ms"]["S30"]:.1f} ms; S90 {measured["S2_p95_ms"]["S90"]:.1f} ms', "< 300 ms", "unfiltered analytical dashboard p95; 3 warmups + 30 runs"),
        check("S3", max(measured["S3_p95_ms"]["S90"]) < 1000, "S90 " + "/".join(f"{value:.1f}" for value in measured["S3_p95_ms"]["S90"]) + " ms", "all < 1000 ms", "filtered analytical dashboard p95 for country, country+source, country+path"),
        check("S4", measured["S4_p95_ms"]["S30"] < 2000, f'{measured["S4_p95_ms"]["S30"]:.1f} ms', "< 2000 ms", "3-step funnel p95; 3 warmups + 30 runs"),
        check("F4", measured["F4_p95_ms"]["S30"] < 1000, f'{measured["F4_p95_ms"]["S30"]:.1f} ms', "< 1000 ms", "filtered signup plan breakdown p95; 3 warmups + 30 runs"),
        check("R1", measured["R1_mb"] < thresholds["R1_mb"], f'{measured["R1_mb"]:.1f} MB', f'< {thresholds["R1_mb"]} MB', "VmRSS after five idle seconds"),
        check("R2", measured["R2_mb"] < thresholds["R2_mb"], f'{measured["R2_mb"]:.1f} MB', f'< {thresholds["R2_mb"]} MB', "peak VmRSS sampled once per second during 1,000 events/s for 60 seconds"),
        check("R5-BINARY", measured["binary_mb"] < thresholds["binary_mb"], f'{measured["binary_mb"]:.2f} MB', f'< {thresholds["binary_mb"]} MB', "built bin/supermetry file size"),
        check("R5-TRACKER", measured["tracker_gzip_bytes"] < thresholds["tracker_gzip_bytes"], f'{measured["tracker_gzip_bytes"]} B gzip', f'< {thresholds["tracker_gzip_bytes"]} B gzip', "gzip level 9 of the served core tracker"),
        check("C1", measured["C1_mismatches"] == thresholds["C1_mismatches"], f'{measured["C1_mismatches"]} mismatches', "0 mismatches; duration within 1%", "30-day totals against deterministic generator truth"),
    ]
    tolerance = 1 + thresholds["regression_tolerance"]
    for metric in ("S2_p95_ms", "S3_p95_ms", "S4_p95_ms", "F4_p95_ms"):
        for tier, baseline in thresholds[metric].items():
            actuals = measured[metric][tier]
            actuals = actuals if isinstance(actuals, list) else [actuals]
            baselines = baseline if isinstance(baseline, list) else [baseline]
            for index, (actual, old) in enumerate(zip(actuals, baselines)):
                limit = old * tolerance
                checks.append(check(f"REG-{metric}-{tier}-{index + 1}", actual <= limit, f"{actual:.1f} ms", f"<= {limit:.1f} ms", "no more than 20% slower than the pinned prototype baseline"))
    return checks


def diagnosis(metric_id):
    if metric_id in {"S2", "S3", "S4", "F4", "DASHBOARD"} or metric_id.startswith("REG-"):
        return "Database time dominates this gate; inspect the captured EXPLAIN plans for excess scans, spills, or a rollup/raw-tier plan mismatch before changing indexes or query fan-out."
    if metric_id in {"R1", "R2"}:
        return "The process exceeded its RSS envelope; inspect the captured process memory map and goroutine dump, then profile the corresponding server phase with a benchmark-only pprof build if retained heap remains unexplained."
    if metric_id == "S1":
        return "The intake path missed throughput, latency, or loss requirements; compare scheduled and service latency plus response codes to distinguish client backlog, backpressure, and database flush limits."
    if metric_id == "STARTUP":
        return "Startup work exceeded the one-second target; use the server log timestamps to separate migration, GeoIP, dictionary, salt, and session-rehydration costs."
    if metric_id.startswith("R5-"):
        return "The release artifact exceeded its size budget; inspect linked binary sections or tracker compression before removing functionality."
    return "Correctness differs from deterministic truth; compare the recorded expected and actual totals before changing product logic."


def render_markdown(result):
    lines = ["# Supermetry product benchmark", "", f'Overall: **{"PASS" if result["pass"] else "FAIL"}**', "", "| Gate | Result | Measured | Threshold | Method |", "|---|---:|---|---|---|"]
    for item in result["checks"]:
        lines.append(f'| {item["id"]} | {"PASS" if item["pass"] else "FAIL"} | {item["value"]} | {item["threshold"]} | {item["method"]} |')
    failures = [item for item in result["checks"] if not item["pass"]]
    if failures:
        lines.extend(["", "## Failures"])
        for item in failures:
            artifacts = result.get("diagnostics", {}).get(item["id"], [])
            suffix = f" Evidence: {', '.join(artifacts)}." if artifacts else ""
            lines.extend(["", f'**{item["id"]}:** {diagnosis(item["id"])}{suffix}'])
    if result.get("dataset_note"):
        lines.extend(["", "## Dataset", "", result["dataset_note"]])
    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--measurements", required=True)
    parser.add_argument("--thresholds", required=True)
    parser.add_argument("--json", required=True)
    parser.add_argument("--markdown", required=True)
    args = parser.parse_args()
    measured_doc = json.loads(Path(args.measurements).read_text(encoding="utf-8"))
    thresholds = json.loads(Path(args.thresholds).read_text(encoding="utf-8"))
    checks = build_checks(measured_doc["measured"], thresholds)
    result = {**measured_doc, "thresholds": thresholds, "checks": checks, "pass": all(item["pass"] for item in checks)}
    Path(args.json).write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    Path(args.markdown).write_text(render_markdown(result), encoding="utf-8")
    print(json.dumps({"result": args.json, "report": args.markdown, "pass": result["pass"], "failures": [item["id"] for item in checks if not item["pass"]]}, indent=2))
    raise SystemExit(0 if result["pass"] else 1)


if __name__ == "__main__":
    main()
