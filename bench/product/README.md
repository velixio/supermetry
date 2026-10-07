# Product benchmark gate

`run.sh tier-s` exercises the built product against the deterministic Tier S
dataset (90 site-local days, seed 42). It uses a fresh disposable PostgreSQL
schema, loads NDJSON through `panel load`, closes site 1, starts `panel serve`,
and measures S2, S3, S4, F4, R1, R2, and C1.

## Run

```sh
PANEL_TEST_DSN='postgres://panel:panel@127.0.0.1:5432/panel?sslmode=disable' \
  make build && bench/product/run.sh tier-s
```

Requirements: PostgreSQL 18, Python 3, curl, psql, the built
`bin/panel`, and the existing `bench/load/bin/{gen,loadtest}` tools. The script
never uses the database's public schema. It creates a uniquely named schema
and drops it on exit.

The result is `bench/results/product-<UTC timestamp>.json`. A run fails when a
MUST limit fails or a measurement is more than 20% slower than
`thresholds.json`. Query timings use three warmups and 30 measured requests.
R2 uses the existing open-loop load tester for 60 seconds at 1,000 events/s;
a local envelope adapter converts its Plausible-shaped request into Panel's
public intake shape without storing or logging IP addresses or User-Agents.

The gate closes only site 1 because every scorecard query and C1 check targets
the largest site. Other sites remain loaded so database size and contention
match Tier S. It refreshes PostgreSQL planner statistics after closing and runs
the server with a 40 MiB Go soft memory limit, matching the prototype's use of
an explicit soft limit for resource measurements.
