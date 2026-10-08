# Product benchmark gate

`run.sh tier-s` exercises the built product against the deterministic Tier S
dataset (90 site-local days, seed 42). It uses a fresh disposable PostgreSQL
schema, loads NDJSON through `panel load`, closes site 1, starts `panel serve`,
and measures startup, S1–S4, F4, R1, R2, R5, and C1. The dashboard timing fires
summary, timeseries, 13 breakdowns, and realtime concurrently.

## Run

```sh
PANEL_TEST_DSN='postgres://panel:panel@127.0.0.1:5432/panel?sslmode=disable' \
  make build && bench/product/run.sh tier-s
```

Requirements: PostgreSQL 18, Go, Python 3, curl, psql, pmap, and the built
`bin/panel`. The script builds its benchmark tools into a temporary directory
under `bench/results/tmp/`. It never uses the database's public schema: a
uniquely named schema is created and dropped on exit, and temporary data is
removed.

The runner refuses to load below 35 GB free and watches disk every five
seconds; below 15 GB it stops all owned processes, drops only its explicitly
prefixed disposable schema, and writes an ABORTED result. A full 100-site load
needs 45 GB at start to preserve the 15 GB reserve. Between 35 and 45 GB, the
stream keeps every event for measured site 1 and omits non-target sites; that
limitation is recorded in both result files.

The outputs are `bench/results/product-<UTC timestamp>.{json,md}`. Every MUST
line is reported as PASS or FAIL, and a run fails when a MUST limit fails or a
measurement is more than 20% slower than `thresholds.json`. Query timings use
three warmups and 30 measured requests. R2 and S1 use the open-loop load tester
for 60 seconds at 1,000 and 10,000 events/s respectively. The tester sends the
Panel intake envelope directly and never logs IP addresses or User-Agents.

When a speed gate fails, the runner records `EXPLAIN (ANALYZE, BUFFERS)`
evidence for representative filtered queries. A memory failure records the
process memory maps and a Go SIGQUIT goroutine dump. Evidence files share the
result timestamp and the Markdown report includes a short diagnosis.

The gate closes only site 1 because every scorecard query and C1 check targets
the largest site. Other sites remain loaded so database size and contention
match Tier S. It refreshes PostgreSQL planner statistics after closing and runs
the server with a 40 MiB Go soft memory limit, matching the prototype's use of
an explicit soft limit for resource measurements.
