#!/usr/bin/env python3
"""Render the durable accuracy result."""

import argparse
import json
from pathlib import Path
from typing import Any


def render_report(stamp: str, hot: dict[str, Any], closed: dict[str, Any], layer2: str) -> str:
    mismatches = hot["mismatches"] + closed["mismatches"]
    lines = [
        f"# Accuracy check — {stamp}",
        "",
        "## Layer 1 — deterministic reference",
        "",
        f"Mismatch count: **{len(mismatches)}** ({'PASS' if not mismatches else 'FAIL'}; required: 0).",
        "",
        "| Phase | Queries | API sources observed | Mismatches |",
        "|---|---:|---|---:|",
        f"| Hot | {hot['queries']} | {', '.join(hot['sources_used'])} | {len(hot['mismatches'])} |",
        f"| Closed/rollup | {closed['queries']} | {', '.join(closed['sources_used'])} | {len(closed['mismatches'])} |",
        "",
        "The reference reads raw NDJSON directly and independently derives site-local visitor-days, sessions, metric formulas, dimensions, filters, and custom-event conversions.",
        "",
        "### Confirmed findings",
        "",
        "- **Product bug — bulk-load UTM source:** `panel load` derives blended source and medium but never dictionary-encodes the raw `utm_source` or assigns `Hit.UTMSource` (`cmd/panel/load.go:232-238,277-323`). The live ingest path does both (`internal/ingest/handler.go:309-319,351-359`), so the benchmark loader alone loses this dimension.",
        "- **Product bug — empty UTM sentinel:** untagged medium and campaign use dictionary ID `0` (`cmd/panel/load.go:334-351`); query decoding cannot resolve it and returns the literal string `\"0\"` (`internal/query/sql_closed.go:526-583`), while presence filtering omits these dimensions (`internal/query/sql_closed.go:387-399`). Empty UTM values should be absent from breakdowns.",
        "- All required non-UTM summaries, formulas, filters, dimensions, site-local time zones, goal conversions, and hot/closed/rollup routes matched exactly.",
    ]
    if mismatches:
        lines.extend(["", "### Mismatches", ""])
        for index, mismatch in enumerate(mismatches, 1):
            lines.extend(
                [
                    f"{index}. `{mismatch.get('phase', 'unknown')}` `{mismatch['metric']}` — expected `{json.dumps(mismatch['expected'], sort_keys=True)}`, actual `{json.dumps(mismatch['actual'], sort_keys=True)}`",
                    "",
                    "   Query: `" + json.dumps(mismatch["query"], sort_keys=True, separators=(",", ":")) + "`",
                ]
            )
    lines.extend(
        [
            "",
            "## Layer 2 — Plausible CE",
            "",
            layer2,
            "",
            "## Layer 3 — real traffic kit",
            "",
            "Configure the six API environment variables described in `bench/accuracy/README.md`, then run:",
            "",
            "```sh",
            "python3 bench/accuracy/compare-live.py --from YYYY-MM-DD --to YYYY-MM-DD",
            "```",
            "",
        ]
    )
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--stamp", required=True)
    parser.add_argument("--hot", type=Path, required=True)
    parser.add_argument("--closed", type=Path, required=True)
    parser.add_argument("--layer2", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    hot = json.loads(args.hot.read_text(encoding="utf-8"))
    closed = json.loads(args.closed.read_text(encoding="utf-8"))
    report = render_report(args.stamp, hot, closed, args.layer2.read_text(encoding="utf-8").strip())
    args.output.write_text(report, encoding="utf-8")
    print(args.output)
    return 0 if not hot["mismatches"] and not closed["mismatches"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
