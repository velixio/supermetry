# Accuracy harness

Layer 1 generates a deterministic 14-day dataset spanning September and
October 2026, keeps sites 1 (New York), 3 (Kolkata), and 5 (Kathmandu), and
computes expected analytics directly from raw NDJSON. It checks the public
query API before and after closing every day, covering hot, closed, visitor
cube, session cube, and rollup routes. The reference is standard-library
Python and imports no product code.

Run from the repository root after building the product:

```sh
make build
PANEL_TEST_DSN='postgres://panel:panel@127.0.0.1:5432/panel?sslmode=disable' \
  bench/accuracy/run.sh
```

The runner creates a unique PostgreSQL schema, never uses `public`, drops the
schema on every exit, and removes temporary NDJSON from
`bench/results/tmp/`. The durable report is
`bench/results/accuracy-<UTC timestamp>.md`.

For shared-host safety, it removes only stale schemas with its
`bench_accuracy_` prefix, refuses to start or load below 35 GB free, and checks
free disk every five seconds. Below 15 GB it terminates the active generator,
loader, server, or checker, records `aborted: low disk`, drops its schema, and
removes its temporary files. Override the thresholds only for testing with
`ACCURACY_MIN_START_GB` and `ACCURACY_MIN_RUN_GB`.

## Real-traffic comparison

Run this once Panel and Plausible trackers receive the same real traffic:

```sh
export PANEL_URL=https://panel.example.com
export PANEL_KEY=panel_stats_key
export PANEL_SITE_ID=1
export PLAUSIBLE_URL=https://plausible.example.com
export PLAUSIBLE_KEY=plausible_stats_key
export PLAUSIBLE_SITE=example.com
python3 bench/accuracy/compare-live.py --from 2026-10-01 --to 2026-10-07
```

The command prints a delta table. Visitors and visits tolerate 2% difference,
bounce rate tolerates 2 percentage points, pageviews and events must be exact,
and top-ten page/source sets must match. Differences can legitimately expose
session timeout, bot filtering, source normalization, or engagement-event
semantics and should be investigated rather than hidden.

## Tests

```sh
python3 -m unittest discover -s bench/accuracy -v
```
