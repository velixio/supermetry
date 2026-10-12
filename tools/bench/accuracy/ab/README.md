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

Before starting the product stacks, this command also runs the deterministic
five-minute live-window fixture and focused Go tests. The fixture covers exact
4:59/5:00/5:01 edges, repeats across minutes, tabs and SPA activity, shared
networks, bots, DNT/GPC, and engagement-only activity.

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

The file lists reviewed, intentional product differences. Today it has one entry:
Plausible Community Edition has no revenue reporting. Add entries with this schema:

```json
{"metric":"bounce_rate","product":"plausible","reason":"concise explanation","evidence":"public issue, documentation URL, or reproducible observation"}
```

`product` is `supermetry` or `plausible`. Entries match only that metric and
product; they never suppress missing data or a broken-tracker result.

## How the products differ, and what the harness does about it

These are not bugs. The comparison accounts for each one, so they never show up
as a mismatch. Each is covered by a unit test in `test_compare3.py`.

| Difference | Handling |
|---|---|
| The **events** metric: Supermetry counts custom events only (pageviews are a separate metric), Plausible counts pageviews and custom events. | Truth carries one value per product (`events_supermetry`, `events_plausible`). |
| Supermetry's tracker sends **Web Vitals** events by itself. They are stored and counted as events. | Excluded from Supermetry's `events` and from its custom event names; they are not scenario traffic. |
| Supermetry reports **direct traffic** as an empty source; Plausible says `(direct)`. | Mapped to `(direct)`. |
| Plausible reports visitors without a campaign as `(not set)`. | Ignored. |
| **Operating system names**: Plausible says `GNU/Linux`, `Mac` and `iPadOS` where Supermetry says `Linux`, `macOS` and `iOS`. | Plausible's names are mapped to Supermetry's. |
| Plausible's `engagement` event is its own time-on-page signal. | Not counted as a custom event. |
| Plausible Community Edition has **no revenue reporting** (`total_revenue` is rejected with HTTP 400). | Listed in `known_differences.json`. |
| A single-page-app navigation to the page the visitor is already on is **not a pageview** in either tracker. | Truth, the driver and the volume run all skip it. |

## Things to know when running it

- **Supermetry keeps referrers, UTM parameters and event properties only for projects that
  opt in** (`profiles_enabled`, on an instance running `SUPERMETRY_TRACKING_MODE=identified`).
  Without that, the source, campaign and property breakdowns are empty by design. The
  harness turns it on for its benchmark project so the comparison is fair; it does not
  change what a default install records.
- Asking Supermetry's query API for `total_revenue` in the same query as a breakdown by
  `event` restricts the rows to events that carry revenue, so the other event names
  disappear. The harness queries revenue on its own.
- The public API is rate limited. While waiting for ingestion to settle, the run polls one
  pageviews query per product, and runs the full set of queries once.

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
