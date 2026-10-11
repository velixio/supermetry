# Soak benchmark

This benchmark drives Supermetry's public event endpoint at a fixed open-loop
rate and checks for memory growth, latency spikes, server errors, queue drops,
and silent event loss.

```sh
python3 tools/bench/soak/soak.py
python3 tools/bench/soak/soak.py --duration 1h --rate 100 --out tools/bench/results
python3 tools/bench/soak/soak.py --smoke
make soak SOAK_ARGS="--smoke"
```

The normal run lasts four hours at 200 events/second. `--smoke` lasts two
minutes at 50 events/second and checks the tooling; it is too short to measure
post-warmup RSS growth meaningfully. Reports are written to the gitignored
`tools/bench/results/soak-<UTC>.md` directory.

## What it does

The runner starts the development PostgreSQL container, recreates only the
dedicated `soak_run` database, builds and starts a fresh server on
`127.0.0.1:18092`, and provisions one `site1.test` site through the normal HTTP
setup flow. Task 303's stack helper was not present on this task branch, so the
runner contains the permitted minimal database and site setup commands rather
than importing it.

Traffic is looped output from `go run ./tools/gen --tier S --days 1 --seed 42`
and is scheduled independently of response completion. Synthetic IP and User-
Agent values are attached to requests but are never written to a log or report.
Ingest limits are raised and recorded in the report so the run measures the
pipeline rather than the limiter.

Every minute the runner records RSS and CPU time from `/proc`, ingest and queue
counters from `/metrics`, database size and stored event count from PostgreSQL,
and client latency/status totals. The server currently exposes neither a Go
runtime goroutine metric nor a debug endpoint, so the report marks goroutines
unavailable. Report rows retain the worst values from each 15-minute window.

After load stops, the runner waits up to 60 seconds for the writer queue to
drain, gracefully terminates the server, and checks stored rows against the
server's accepted counter. The server process and `soak_run` database are
always removed, including after failures. The development container itself is
left running, matching other development commands.

Do not use the owner's server, port 8787, or the `supermetry` database for this
benchmark. A real soak is intentionally not part of the automated test suite.

