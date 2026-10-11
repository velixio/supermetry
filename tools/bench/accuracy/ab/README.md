# Accuracy A/B benchmark

This benchmark sends identical traffic to Supermetry and Plausible Community
Edition, then compares both products with independently computed truth. It
checks aggregate counts, acquisition and device breakdowns, custom events, and
revenue. It does not test real visitors, geography, imports, heatmaps, session
replay, or relative performance.

## Prerequisites

- Docker with about 3 GB free for the Plausible and ClickHouse images
- Go and Bun
- Chromium installed with `cd tools/bench/accuracy/ab/driver && bunx playwright install chromium`
- Ports 8000, 18090, and 18091 available

Run the complete benchmark from the repository root:

```sh
make accuracy-ab
```

The command starts isolated product stacks, drives browser and volume traffic,
waits for ingestion to settle, writes timestamped Markdown and JSON reports
under `tools/bench/results/`, and tears the stacks down. It returns non-zero for
an unexplained difference, a tracker that recorded less than half of the
expected pageviews, or an earlier run failure.

For troubleshooting, run the pieces from the repository root:

```sh
python3 tools/bench/accuracy/ab/stack.py up
(cd tools/bench/accuracy/ab/driver && bun run run.ts --visits ../../../results/accuracy-ab/visits.json --out ../../../results/accuracy-ab/driver-report.json)
python3 tools/bench/accuracy/ab/volume.py --scenario tools/bench/results/accuracy-ab/volume-visits.json --stack tools/bench/results/accuracy-ab/stack.json --report tools/bench/results/accuracy-ab/volume-report.json
python3 tools/bench/accuracy/ab/stack.py down
```

The driver command expects the site server from `../site/server.ts` to be
running with the four tracker environment variables described by `stack.json`.
The complete orchestrator starts and stops that server automatically.

`MATCH` means both products are within the metric's tolerance. `EXPLAINED`
means every mismatch is a reviewed entry in `known_differences.json`.
`UNEXPLAINED` needs investigation. `BROKEN` means at least one tracker recorded
less than half of truth, commonly because browser automation, the host name,
Do-Not-Track, or the user agent suppressed it. `NOT STABLE` means totals kept
changing for the full two-minute wait; the report is still written, but its
differences are not reliable.

## Known differences

The file starts as an empty JSON array. Add only reviewed, intentional product
differences with this schema:

```json
{"metric":"bounce_rate","product":"plausible","reason":"concise explanation","evidence":"public issue, documentation URL, or reproducible observation"}
```

`product` is `supermetry` or `plausible`. Entries match only that metric and
product; they never suppress missing data or a broken-tracker result.

## Metric definitions

Both truth variants define a bounce as a visit with exactly one pageview and no
custom event. Supermetry implements that definition in
`internal/session/memory.go` and `internal/closer/rollup.go`. The Plausible
definition is the accepted contract in the benchmark design. Bounce rates are
compared within two percentage points; visitors and visits within two percent;
all other values are exact, with top-ten rows compared as sets.

Plausible custom events are queried through the v2 Stats API's `event:name`
dimension. That reads event names directly, so the benchmark does not need to
register each generated event as a goal.

After this controlled run, compare a real site's existing traffic separately:

```sh
SUPERMETRY_URL=https://supermetry.example.test SUPERMETRY_KEY=key SUPERMETRY_SITE_ID=1 \
PLAUSIBLE_URL=https://plausible.example.test PLAUSIBLE_KEY=key PLAUSIBLE_SITE=site.example.test \
python3 tools/bench/accuracy/compare-live.py --from 2026-10-01 --to 2026-10-07
```
