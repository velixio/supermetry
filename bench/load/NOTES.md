# bench/load: notes (load agent, gate step 4)

Loaders, query harness and HTTP load tester for the incumbents, so the scorecard's REL lines can be filled in against the Supermetry prototype on **the same data** (gen Tier S, seed 42, 30 days, 2026-09-01..30, 30,296,439 events) and **the same query set** (bench/proto: S2, S3 with 1 and 2 filters, S4, F4; biggest site = site 1, America/New_York).
Results report `incumbents-queries-20261007.md` removed 2026-10-10 (in git at `7e7294d`); headline numbers are in progress.md. Throwaway code; Go 1.26 module `supermetry/bench/load` (pgx/v5 only).

## Layout

| Path | What |
|---|---|
| `internal/rec` | Parallel, order-preserving NDJSON decoder for the gen stream |
| `internal/pgcopy` | Text-format COPY batches over N connections |
| `umami/` | `main.go` loader (COPY into `website_event`, `session`, `event_data`); `load.sh` drop indexes → COPY → re-create the same 33 index/PK DDL → VACUUM ANALYZE |
| `plausible/` | `main.go` loader (TabSeparated into `clickhouse-client` in the CH container: `events_v2`, `sessions_v2`); `load.sh` up → sync zones → truncate → load → wait for merges → goal `signup` |
| `goatcounter/` | `main.go` importer through `/api/v0/count` (created_at = event time); `serve.sh` start/stop serve with a `-ratelimit` override; `load.sh` |
| `harness/` | `harness bench` (dashboard timing through each product's HTTP API) and `harness c1` (overview vs truth.json) |
| `loadtest/` | Open-loop HTTP ingest load tester (R2, S1), same method as the prototype's |
| `memsample.sh`, `ingest.sh` | R2 memory sampler (uses `../rig/memstat.py`), R2/S1 driver |
| `data/` (ignored) | `sites.json`, `truth-S30.json`, logs, saved Umami index DDL, API keys/tokens (600) |
| `bin/` (ignored) | built binaries, incl. `gen` built from `../gen` |

Raw NDJSON is never stored: `bin/gen --tier S --days 30 --seed 42 --out - | loader`.

## How each product was loaded (2026-10-07)

### Umami 3.4.0 (local PG 18, db `umami`)
- `umami/load.sh 30`: saves Umami's own index/PK DDL from `pg_indexes`/`pg_constraint` (33 = 15 on `website_event`, 12 on `session`, 6 on `event_data`; `data/umami-index-ddl.sql`), truncates, drops them, COPYs over 4+1+1 connections (338K ev/s, 90 s), re-creates **exactly the same 33** (507 s, 4 parallel sessions, `maintenance_work_mem` 1 GB per session), then `VACUUM (ANALYZE)` (77 s).
  - Bulk-built btrees are denser than ones grown by Umami's random-UUID inserts, so R3 is a **lower bound** for an organically grown Umami DB (conservative for Supermetry's "≤ ½ of Umami").
- Identity (emulates `/api/send`, MIT code not copied): `session_id` = UUID(website, ip, ua, UTC month), one `session` row per session_id (Umami's monthly-salted "visitor"); `visit_id` = UUID(sk) (true session; CONTRACT allows sk); `event_id` = UUID(sk, ts) (uniform like v4).
- Value formats probed through the real `/api/send` before loading: `referrer_domain` = host without `www.`, NULL for internal/none; `url_query` '' when empty; utm_* NULL when empty; browser/os labels (`chrome`, `ios`, `crios`, `edge-chromium`, `samsung`, `Android OS`, `Windows 10`, `Mac OS`); `event_data` string rows (data_type 1), 3 per custom event.
- Not filled (conservative for R3): `page_title`, `region`, `city`; `language` = `en-US`.
- **PG role settings**: `ALTER ROLE umami SET work_mem = '32MB'`, `max_parallel_workers_per_gather = 4` (= Supermetry's session-tuned GUCs), and **`TimeZone = 'UTC'`**: the local server's default zone is America/New_York and Umami/Prisma binds naive UTC timestamps, so without it every query window was shifted by the NY offset (found via a temporary per-role `log_min_duration_statement`, since reset). Umami's own compose runs PG in UTC.
- Counts after load: 30,296,439 events, 5,743,273 sessions, 9,081,966 event_data rows, 33 valid indexes (re-checked after the 06:13 reboot).

### Plausible CE v3.2.1 (its compose ClickHouse 24.12 + PG 16)
- `plausible/load.sh 30`: `up.sh`, `sync-timezones.sh data/sites.json plausible`, truncate `events_v2`/`sessions_v2` (also drops the rig's probe rows), pipe gen → `plausible-load` → two `clickhouse-client` INSERT … FORMAT TabSeparated streams inside the CH container (383K ev/s, 79 s). 30,296,439 events, 10,928,956 sessions (= truth).
- `user_id` = hash(site, vk, site-local date) (daily rotation, at local midnight to match the truth); `session_id` = hash(sk); one sessions_v2 row per session, `sign = 1`, final values; every event carries its session's referrer/source/UTMs (as the live ingest stores them); whole-second timestamps.
- referrer_source names probed via `/api/event` (Google, Bing, DuckDuckGo, Facebook, Twitter for t.co, Reddit, LinkedIn, Hacker News, GitHub, Youtube, StackOverflow, Instagram, Ecosia, Perplexity, Baidu, Yandex; chatgpt.com/dev.to/medium.com/blogs keep the host); utm_source wins; direct = ''. `referrer` = host without www + path. screen_size from width (<576 Mobile, <992 Tablet, <1440 Laptop, else Desktop); OS `Mac`, `GNU/Linux`; browser `Microsoft Edge`, `Samsung Browser`.
- Needed so the history is queryable: `UPDATE sites SET native_stats_start_at = '2026-08-30', stats_start_date = '2026-08-30'` (Plausible clamps queries to the site's creation time; otherwise every query returned 0).
- Stats API key created via release RPC (`Plausible.Auth.ApiKey.changeset(%ApiKey{user_id: 1}, team, %{name, key})`), stored in `data/plausible-api-key` (600). **`hourly_request_limit` raised 1000 → 1,000,000** (the bench sends ~1,500 requests/h). Goal `signup` created for F4.
- CE's default low-resources ClickHouse profile left on (`max_threads = 1`).

### GoatCounter v2.7.0 (local PG 18, db `goatcounter`)
- Import through `/api/v0/count` with `created_at` (the API `goatcounter import` uses). **Sequential only**: with 8 concurrent requests v2.7.0 panics (`sync: WaitGroup is reused before previous Wait has returned`, HTTP 500/504); that partial import was purged and the 100 sites re-seeded (`down.sh --purge`, `up.sh`, `seed.sh goatcounter 100`, `sync-timezones.sh`).
- Throughput sequential, 500 hits/request: ~7.3K hits/s, i.e. ~70 min for all 30.3M events, so a **documented subset** was imported: sites 1, 3, 5 (the queried site plus the two C1 sample sites).
- Tokens: `goatcounter db create apitoken -perm count,site_read` per site user (each site has its own user because the zone is per user), then `permissions = 74` via SQL: the CLI has no `stats` permission (the UI has); 64 = stats was found empirically.
- serve runs from `goatcounter/serve.sh` (same flags as `up.sh`) with `-ratelimit count:1000000/1,api:1000000/1,api-count:1000000/1` for import and queries (API default `api:4/1` would 429 a 7-panel dashboard; `goatcounter help import` itself suggests raising it for imports). Ingest tests (R2/S1) use the default rate limit.
- `location` = record country, `session` = hash(site, vk, local date), size = width, custom events as `event: true` with path = event name (props dropped).
- Imported: site 1 9,127,979 events (948 s, 9.6K/s), sites 3+5 3,518,877 (675 s, 5.2K/s), 0 failed requests. Even sequential, GoatCounter logged 16 PG deadlocks in its `cron.update*` aggregation; `gcexpect/` computes the expected hit_counts totals from the stream: site 1 8,556,487 expected vs 8,551,740 stored (−0.055%), sites 3/5 −1/0.

## Harness (`harness bench|c1`)
- The prototype's cases, panels fired concurrently, wall time per dashboard; warm-up then 30 runs; `-budget` would reduce runs for very slow cases (not triggered: Umami's S2 took ~20 min for 32 runs).
- Supermetry → API: Umami `stats`, `pageviews?unit=day`, `metrics?type=path|referrer|country|browser|os|device|entry|exit&limit=50`, filters `country=eq.US`, `path=eq./`, `referrer=eq.<host>`; funnel `POST /api/reports/funnel` (window 60); props `event-data/values`. Plausible: one `/api/v2/query` per panel (metrics visitors+pageviews; entry/exit visitors+visits; overview visitors, visits, pageviews, bounce_rate, visit_duration; `time:day`), filters `visit:country`, `visit:source` ("Direct / None"), `event:page`; props `event:props:plan` with `event:goal = signup`. GoatCounter: `stats/total` (also the daily series), `stats/hits`, `stats/{toprefs,locations,browsers,systems,sizes}`, path filter `include_paths` + `path_by_name`; ranges rounded to whole UTC hours.
- C1 converts Plausible's bounce_rate (%) and visit_duration (mean s) back to totals, so their error includes the API's rounding.

## Ingest tests (`ingest.sh`, `loadtest/`, `memsample.sh`)
- Feed: gen Tier S day 2026-10-01 (after the loaded range); ip → `X-Forwarded-For`, ua → `User-Agent`, real paths/refs/props. Open loop, latency from the scheduled time, 256 workers, 5 s timeout, overflow counted.
- R2: 20 s idle, then 1K ev/s for 5 min, memory every 10 s (app processes, the product's PG backends, the shared PG server's non-client processes, `docker stats`). S1: 30 s steps.

## Gotchas found
- Local PG's default `TimeZone` is America/New_York; Umami (Prisma) needs UTC (role setting).
- Plausible ignores history before `sites.native_stats_start_at`.
- GoatCounter 2.7.0: concurrent `/api/v0/count` → panic; `-perm` has no `stats`; `size` must be a string; at ≥ 24K ev/s `/count` still answers 200 but the memstore logs duplicate-key errors.
- `ingest.sh`: `gen | loadtest` under `pipefail` exits non-zero by SIGPIPE; handled with `|| true`.

## State at hand-off (2026-10-07 03:13 UTC)
All products stopped. Umami and Plausible purged (site maps copied to `data/umami-sites.json`, `data/plausible-sites.json`). GoatCounter data kept (sites 1, 3, 5 loaded; db 2.6 GB; `data/goatcounter-tokens.json` valid for it). `goatcounter` role keeps `work_mem=32MB`, `max_parallel_workers_per_gather=4`.
Not done: Tier S 90 d (Umami would need ~90 GB), GoatCounter's other 97 sites, a quiet-box rerun.
