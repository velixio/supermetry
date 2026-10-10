# bench/proto: Supermetry prototype notes

The throwaway prototype for gate steps 4–5 ([../CONTRACT.md](../CONTRACT.md), [../../scorecard.md](../../scorecard.md)). It is Go 1.26 module `panel/bench/proto`, using pgx/v5 against the local PG 18 database `panel` (role `panel`/`panel`, created with `sudo -u postgres`). PG was neither restarted nor reconfigured globally; the only settings are per role (`ALTER ROLE panel SET work_mem = '64MB', jit = off`, see "Query model").

It faithfully implements the planned hot path (goal.md "Architecture") and query model (D4–D8), plus the closed-day tier added by the optimisation pass of 2026-10-07. It is not product code.

## Usage

```sh
go build -o proto .                      # 18 MB static-ish binary
go test ./...                            # needs local PG; DB tests use a throwaway schema
./proto load --sites sites.json < events.ndjson     # bulk load through the ingest pipeline
SUPERMETRY_RAW_PARTITION=daily ./proto load ...           # O5 only: daily raw-event partitions
./proto retention --raw-days 7 [--as-of 2026-07-21] # DROP expired raw-event partitions
./proto rollup --from 2026-09-01 --to 2026-09-30 [--parallel 4] [--drop-hot]
                                                    # close days: closed-day tables + rollups (idempotent), monthly rollups
./proto rollup-months --from 2026-07-01 --to 2026-07-31 [--site N]   # rebuild monthly rollups of complete months
./proto rollup-blocks --from 2025-10-01 --to 2026-09-30 [--site N]  # backfill dyadic blocks over the full closed span
./proto rollup-stream --sites sites.json < events.ndjson   # rollups straight from NDJSON, no raw rows
./proto verify --truth truth.json [--raw-every N] [--workers 8]   # C1: exact counts, duration ±1%
./proto query sources --site 1 --from 2026-09-01 --to 2026-09-30 --filter country=US --filter path=/pricing
./proto query dashboard --site 1 --from ... --to ... --filter country=US  # all panels in one call
./proto query realtime --site 1                     # asks the running server (from memory)
./proto serve [--memlimit-mb 100]                   # :8090, POST /api/event, GET /api/query/{name}, /api/realtime, /api/stats
./proto bench --tier S --s2-days 30,90 --s3-days 90 --kind proto-opt-queries [--mode hot] [--fanout 8]
              [--no-grouping] [--dashboard=false] [--no-monthly] [--sample 16] [--cases s2,s3,s4,f4]
./proto copybench --indexes none,site_ts,custom     # COPY micro-benchmark → ../results/
./proto loadtest --rate 10000 --duration 30s [--churn]   # HTTP ingest → ../results/
./proto reset                                       # drop raw and closed partitions, truncate rollups, salts, dict
```

Env: `SUPERMETRY_DSN` (default `postgres://supermetry:supermetry@127.0.0.1:5432/supermetry?sslmode=disable`). Extra DSN params become session GUCs. `SUPERMETRY_PPROF=127.0.0.1:6061` serves pprof. `SUPERMETRY_DEBUG_SQL=1` prints each closed-tier statement's time.

Query names: `overview timeseries pages sources countries browsers os devices entry exit funnel props realtime` (+ `dashboard` in `proto query`). Filters: `path source country browser os device entry exit utm_source utm_medium utm_campaign`, combinable (AND). `--mode auto|rollup|raw|hot`.

## Dependencies (all permissive)

| Module | License | Why |
|---|---|---|
| `github.com/jackc/pgx/v5` | MIT | PG driver, CopyFrom |
| `github.com/mileusna/useragent` | MIT | UA parser. It is hand-written, not regex-based, at ~1.6 µs per UA+GeoIP lookup, and tiny in memory, which suits R1/R2. uap-go (Apache-2.0, regex corpus) is more exhaustive but costs ~10–20 MB RSS and tens of µs. |
| `github.com/oschwald/maxminddb-golang/v2` | ISC | mmdb reader (mmap) |
| `golang.org/x/sync` | BSD-3 | errgroup (closed-tier fan-out, parallel verify) |
| DB-IP **IP to Country Lite** mmdb | **CC-BY 4.0**: attribution "IP Geolocation by DB-IP" (https://db-ip.com) is required wherever the data is shown | GeoIP, realistic cost (8.3 MB, mmap'd). `curl -L https://download.db-ip.com/free/dbip-country-lite-2026-10.mmdb.gz \| gunzip > data/dbip-country-lite.mmdb`. `data/` is gitignored. |

No code was copied from any reference repo. All of the code here is new.

## Schema (forward-only migrations run at start; `internal/store/schema.go`)

Migration 1, the hot tier:

```text
sites(id int PK, host text UNIQUE, tz text)
salts(site_id, day date, salt bytea, created_at)            PK (site_id, day); purged after 48 h
events   PARTITION BY RANGE (ts), monthly (events_pYYYY_MM) by default;
         daily (events_dYYYYMMDD) with SUPERMETRY_RAW_PARTITION=daily for O5
  site_id int, local_date date, ts timestamptz, visitor_id bigint, session_id bigint,
  name text, path text, source, country, browser, os, device text NOT NULL,   -- session dims copied on
  utm_source/medium/campaign text NULL, props jsonb NULL
  indexes: BRIN (ts) pages_per_range=32 autosummarize; partial btree (site_id, ts) WHERE name <> 'pageview' (m3)
sessions PARTITION BY RANGE (start_ts), DAILY (sessions_dYYYYMMDD; older data may sit in monthly sessions_pYYYY_MM),
  fillfactor 90 (HOT updates while active)
  site_id, local_date, start_ts, session_id, visitor_id, duration_ms int, pageviews int, custom_events int,
  entry_path, exit_path, source, country, browser, os, device, utm_* (NULL)
  PK (site_id, start_ts, session_id) (needed by the upsert); BRIN (start_ts) autosummarize
rollup_day(site_id, day, visitors, visits, pageviews, bounces, duration_ms)   PK (site_id, day)
rollup_day_dim(site_id, dim smallint, day, value text, visitors, visits, pageviews, bounces, duration_ms)
  PK (site_id, dim, day, value); dim 1 path 2 source 3 country 4 browser 5 os 6 device 7 entry 8 exit
```

Migrations 2–7, the closed tier (built when a site-local day closes, see "Closing a day"):

```text
dict(id int identity PK, value text UNIQUE)          one id space for every string; id 0 = ''
sessions_closed PARTITION BY RANGE (day), monthly     one row per session of a closed day, 148 B/row
  visitor_id, session_id bigint, site_id, day, source, country, browser, os, device, entry, exit,
  utm_source, utm_medium, utm_campaign int (dict ids), pageviews, custom, duration_ms int,
  vd_n smallint   -- sessions of the visitor-day
  flags smallint  -- 1 first session of the visitor-day, 2 first with its source, 4 entry, 8 exit
  seq int[]       -- the session's events in order: pageview = path_id<<2 | 2 (first view of the path in
                  --   the session) | 1 (first in the visitor-day); custom event = -name_id
  rows sorted by (site, country, visitor_id) within a day; BRIN (site_id, day, country, visitor_id)
cube_day PARTITION BY RANGE (day)                    daily session cube, 84 B/row
  site_id, day, source, country, browser, os, device, v_vd, v_src, visits, pageviews, bounces, duration_ms
custom_closed PARTITION BY RANGE (day)               custom events of closed days with their session's dims + props
rollup_month_dim(site_id, dim, month, value, …bigint) PK (site_id, dim, month, value); rollup_month_done(site_id, month)
rollup_block_dim(site_id, dim, level 1..9, block_start, value, …bigint)
  PK (site_id, dim, level, block_start, value)
rollup_block_done(site_id, level 0..9, block_start)    completed blocks, including empty ones
```

Country, browser, os and device are visitor-day attributes in the closed tier: those of the visitor-day's first session (they derive from ip+ua, which defines the visitor, so they never differ in practice; gen's CONTRACT reading 1 says the same).

Btree indexes "proved needed": the sessions PK (upsert target) and the partial custom-event btree (F4 on hot days; COPY cost ≈ 0, +4 B/event, `proto-opt-copybench-20261007-074926.json`). A full `events (site_id, ts)` btree costs 33–40% of COPY throughput and +33–41 B/event and is still not added (small sites are served by the closed tier now).

**Fixed bug**: the BRIN indexes had no summaries after a bulk load (summaries are only made by VACUUM unless `autosummarize = on`), so every "range" scan read the whole partition: a 2-day window read 4.1 GB instead of 0.3 GB (9.7 s → 2.3 s). Migration 2 recreates them with `autosummarize = on`, and `load` now calls `brin_summarize_new_values` after ANALYZE.

## Hot path (`internal/ingest`, `internal/core`)

`POST /api/event` → `Builder` → `Pipeline.TrySubmit` → bounded channel → N batchers → `COPY events`; a flusher upserts dirty sessions.

1. **Body**: Plausible-like `{n,u,d,r,p}`. `p` may be an object or a JSON string. The IP comes from `X-Forwarded-For` (trusted here, for load tests) or RemoteAddr; the UA comes from the header. The body is capped at 8 KB.
2. **Builder**: site by host → **real enrichment** (UA parse, GeoIP; bots → 202 and dropped) → site-local day (`time.Location`) → salt (an in-memory map, no DB read) → `visitor_id` → source (`utm_source`, otherwise the lowercased referrer host; self-referrals and an empty referrer count as direct `""`). IP and UA are dropped here and never reach an `Event` (P1).
3. **visitor_id** = first 8 bytes of `HMAC-SHA256(salt[site, local date], site_id(4B BE) ‖ ip ‖ 0x00 ‖ ua)`, as a bigint. The 64-bit collision chance for 1M visitors/day is ~3·10⁻⁸ per site-day.
4. **Salts**: random 32 B from crypto/rand per (site, local date). The server pre-creates today's salt at start, and tomorrow's in the last hour of each site's day; new salts are persisted asynchronously by the scheduler (`INSERT … ON CONFLICT DO NOTHING`); salts are loaded at start and purged from PG after 48 h and from memory after the local day ends + 1.
5. **Session cache**: 64 mutex shards keyed by `visitor_id % 64`, one live session per visitor. If the next event arrives ≤ 30 min after `Last` it continues the session; otherwise a new one starts. Because the visitor ID changes at local midnight, sessions never cross it. Session dims come from the first event and are **copied onto every event**. Entry is the first pageview, exit the last; bounce = 1 pageview and 0 custom events; duration = last − first.
   - **Compact `Session`, 80 B** (was 128 B + per-request strings; R2): the session ID is not stored but derived as a 63-bit mix of (visitor ID, start µs); entry, exit and source are `unique.Handle[string]` (8 B, one canonical copy per distinct path/host, reclaimed by the GC); country/browser/os/device are 2-byte interned IDs; the day is a uint16. `TestSessionMemory`: 206 → 116 heap B per live session (incl. the map slot).
   - `serve --memlimit-mb 100` (default) sets the Go soft memory limit unless `GOMEMLIMIT` is set.
6. **Atomic accept**: `Cache.Assign(ev, commit)` runs the non-blocking channel send *under the shard lock*. On a full queue the session is left untouched and the handler returns **503 + Retry-After**. It never blocks (tested).
7. **Batchers** (default 2 writers, 5000 events or 1 s) run `CopyFrom`, retrying with backoff while the queue fills, so producers get 503s. Partitions are created on demand (`CREATE TABLE IF NOT EXISTS … PARTITION OF`; a range already covered by an older partition counts as existing).
8. **Sessions**: dirty sessions are tracked per shard. Every 10 s (or every 250K events) four goroutines each take 1/4 of the shards and upsert ≤ 20K-row chunks (COPY into a temp stage, then `INSERT … ON CONFLICT DO UPDATE`). Idle sessions are evicted in the same pass. **Session backpressure**: the bulk `Submit` waits once 500K accepted events await a session flush, and `TrySubmit` returns 503 above 2M.
9. **Shutdown**: SIGINT/SIGTERM → `http.Server.Shutdown` → close the queue (under a write lock, so no send-on-closed) → batchers drain and flush → final upsert of every dirty session. The 30 s deadline cancels in-flight writes and counts them as dropped.
10. **Restart rehydration**: before accepting traffic, `serve` finds session IDs that are open relative to `max(now, newest persisted event)` and rebuilds their complete start/last/pageview/custom/entry/exit state from durable `events`. It also considers recently updated `sessions` rows, because a session flush can race ahead of event COPY; those rows are replaced with event-derived state or removed when none of their events were durable. Only sessions whose durable last event is within 30 minutes are restored to the cache. Buffered events lost to `kill -9` therefore disappear instead of inflating the session, while later events retain the durable session ID even if the original start event was among those lost. The rare ID override lives in `sessionExt`, so the 80 B hot-path `Session` stays unchanged. This startup-only reconciliation adds no DB read to the ingest path and stores no IP or full UA (C2).

**Bulk load** (`proto load`) uses the same Builder → Cache → bounded queue → COPY path. NDJSON is parsed by GOMAXPROCS−2 workers in 2048-line chunks and **re-sequenced in input order** before sessionization. `Submit` blocks instead of answering 503. The pipeline's eviction clock is the event-time watermark. After loading, touched partitions are ANALYZEd and their BRIN ranges summarized.

## Closing a day (`store/rollup.go`, `proto rollup`, the server's scheduler)

`RecomputeDay(d, sites)` runs 2 min after each site's local midnight (scheduler) or from `proto rollup` (`--parallel N` days at once). It is idempotent:

1. new strings of the day go into `dict` (own short transaction, so concurrent days don't wait on each other);
2. in one transaction (`SET LOCAL enable_nestloop = off, enable_mergejoin = off, work_mem = 128MB`: both a nested loop on stale stats and a merge join on `site_id` alone were quadratic here): delete the day's closed rows and rollups; build `sessions_closed` from one pass over the day's events (two `row_number()` windows give the first-in-session / first-in-visitor-day bits, `array_agg … ORDER BY ts` gives `seq`) and one over its sessions (windows give `vd_n`, the flags and the visitor-day dims); copy the custom events into `custom_closed`;
3. derive `rollup_day`, `rollup_day_dim` and `cube_day` from `sessions_closed`, without any distinct-visitor aggregation (visitors = rows with the right flag); then idempotently rebuild the day's completed dyadic ancestors.

Tier S: ~16 s per day with 4 days in parallel (90 days in 9.6 min) on the shared box. Tier M: see results.

`--drop-hot` drops hot `sessions` partitions behind the prefix of closed days (`DropCompactedSessions`: UTC day U holds local dates U−1..U+1). The closed tier answers everything for closed days, so the hot tier is a ~3-day buffer; this kept Tier M under 33 GB.

`RecomputeMonth(month, sites)` sums the daily rows into `rollup_month_dim` and marks the month done; the scheduler runs it after a month's last day, `proto rollup` for every month the run covered, `proto rollup-months` on demand.

`rollup-stream` (`core/aggregate.go`, `ingest/stream.go`) still builds the same rollups straight from NDJSON (no closed tables).

## Query model (`internal/query`)

Tiers: **rollups** (unfiltered closed days), the **closed tier** (filtered closed days, funnels, props), the **hot tier** (today; `--mode hot` forces it for everything, which reproduces the pre-optimisation queries). Results of the tiers add up (visitor IDs rotate daily, so visitors are additive across days, F1).

- **Auto mode**: unfiltered panels read rollups for closed days, plus the hot tier for today. Any filter routes closed days to the closed tier (`closedThrough` = last day with `cube_day` rows, cached 10 s) and today to the hot tier.
- **Rollups**: totals and timeseries read the small `rollup_day` table. Breakdowns use complete monthly rows plus epoch-aligned `2^k`-day edge blocks (`k=0..9`; `rollup_day_dim` is level 0). Missing blocks recursively fall back to completed children, so pre-tracking dates, empty days and in-progress ancestors stay exact. Monthly-plus-dyadic is the measured faster default; `bench --no-monthly` measures pure dyadic. Ordering: visitors desc, pageviews desc, value asc `COLLATE "C"`.
- **Closed tier** (`query/closed.go`), visitors without a distinct-visitor aggregation whenever exact:
  - `cube_day` answers overview, timeseries, sources, countries, browsers, os and devices when the filters use only visitor-level dims and source: each visitor-day is in exactly one cell (`v_vd`), or in one cell per source it arrived from (`v_src`).
  - `sessions_closed` with a **first flag**: exact when the filters select whole visitor-days plus at most one session-level dimension D (source, entry, exit or path) and the panel is visitor-level or D itself.
  - otherwise the **split de-duplication**: rows of single-session visitor-days (`vd_n = 1`) count directly, only the rest are grouped by `(value, visitor_id)`.
  - pages, entry and exit come from **one pass over the unnested `seq`**, keyed by path id: a session's entry (exit) is attributed to the first-in-session element of its entry (exit) path.
  - with a path filter, every session panel comes from one GROUPING SETS pass; the matching pageviews are counted from `seq`.
  - **Fan-out**: a range is cut into 8 day chunks run concurrently (`Engine.Fanout`) and summed in Go; values are decoded through an in-process dict cache, then sorted and cut.
  - `Engine.Dashboard` computes all 10 panels together: per chunk one cube statement + one sessions_closed statement.
- **Hot tier** (unchanged): session-dim filters on sessions/copied event columns, entry/exit as a semi-join (also into `sessions_closed`), `path` as a page filter; visitors = `count(*)` over `GROUP BY visitor_id`.
- **Funnel F**: closed days from `seq` with `array_position` (first `/` view, first `/pricing` after it, first `signup` after that: the generator's greedy in-order match); hot days with the old MATERIALIZED CTE.
- **Props**: closed days from `custom_closed`, hot days from `events` (partial btree).
- **Approximate mode (opt-in, never the default)**: `Engine.Sample = N` (`bench --sample N`) makes every sessions_closed statement read the lowest 1/N of the visitor-ID space (contiguous within each (day, site, country) block, BRIN-skippable) and scales counts by N; the cube stays exact.
- **Per-role settings** (approach 6): `ALTER ROLE panel SET work_mem = '64MB'; ALTER ROLE panel SET jit = off;` (`max_parallel_workers_per_gather` stays 2: 0 and 4 were slower).
- **Realtime**: the number of cached sessions of the site with `Last ≥ now − 5 min`.

## Deviations (documented)

1. **`load` uses the record's parsed dims by default** (`--enrich truth`). `--enrich real` runs the real UA parser + GeoIP (the HTTP path always does). Sessionization and hashing are identical in both modes.
2. **Historical salts** (load, rollup-stream) are random but kept in memory only, so a local day split across two `load` runs would double-count its visitors. `vk`/`sk` are never decoded.
3. `X-Forwarded-For` is trusted unconditionally (load testing from one host).
4. `events` has no primary key or event id (append-only; client retry deduplication is out of scope). C2 crash recovery treats the durable event rows as authoritative for session state.
5. Hot-tier top pages return no bounces/duration; the closed tier and rollups do (same definition: the sessions that viewed the page).
6. Self-referrals count as direct. Direct is stored as `""` and shown as `(direct)` by verify.
7. Funnel steps: the hot tier needs a strictly later `ts`; the closed tier uses `seq` order (ties in `ts` keep arbitrary order). The generator's timestamps are strictly increasing per visitor.
8. Session IDs are a hash of (visitor, start), not time-ordered; the sessions PK still leads with `start_ts`.
9. With a path filter, the closed-tier funnel keeps sessions that viewed the path (the oracle's semantics); the hot funnel restricts events to the path.
10. Migration 2 was edited once while only empty tables depended on it (dev DB fixed by dropping them); forward-only from here on.
11. `proto retention --raw-days N` drops only complete raw-event partitions. Monthly is the production default; daily is an explicit benchmark option that makes a seven-day plateau visible. Multi-instance/Redis, auth, UI and hourly rollups remain out of scope. C2 is demonstrated for single-instance hard and clean restarts.

## Fixture (`testdata/fixture.ndjson`, hand-computed)

There are 20 events on 2 sites (`a.test` UTC, `b.test` Asia/Kathmandu +5:45). They cover a 25-min gap (same session) and a 35-min gap (new session), a local-midnight split with a 2-min gap (new day → new visitor → new session), a UTC-date ≠ local-date event, utm_source over the referrer, self-referral → direct, a bounce versus 1 pageview + 1 custom event (not a bounce), and funnel cases including `/pricing` before `/` (step 1 only). Expected values are in `internal/ref/ref_test.go`, e.g. a.test 07-01: visitors 3, visits 4, pageviews 8, bounces 1, duration 2819 s, funnel [3,2,1].

## Tests (`go test ./...`, all with `-race`)

- `core`: local dates for +5:30, +5:45 and DST zones; visitor hash determinism and scope; salts; sessionization gap rule (25/30/30+ min); dims copied from the first event; pageviews/custom/entry/exit/bounce/duration; reject leaves state unchanged; eviction and replacement; realtime; path tracking; a per-session memory probe.
- `core/query/store`: dyadic decomposition at single-day, month, leap-day and DST boundaries (Kolkata, Kathmandu, New York); exact block sums versus daily sums; incomplete-block fallback; idempotent rebuild and historical-day ancestor refresh.
- `ref`: the oracle against hand-computed fixture numbers.
- `enrich`: UA labels, bot, GeoIP lookups (8.8.8.8 → US).
- `ingest`: backpressure (TrySubmit never blocks, rejected events don't touch sessions, flush on close); close deadline; Submit blocks until space or ctx; periodic flush and idle eviction; session-backlog backpressure; HTTP 202/503/400.
- privacy/restart: injected `crypto/rand` failures, exact 48-hour salt cutoff with a fake clock, cross-site visitor scoping, malicious client IDs/flags ignored, open-session rehydration, whole-partition retention, and HTTP hard/clean restart scripts.
- `tracker`: source/minified parity, pageview + SPA + custom-event contract, forbidden storage API scan, and deterministic gzip budget.
- `e2e` (PG, throwaway schema), every check against the oracle:
  - rollups, **closed tier** (`raw`, with 2-chunk fan-out + GROUPING SETS and with one statement per panel), **hot tier** and the **Dashboard** call, for every site/day and range;
  - every single filter value plus 2-filter combos (country×path, country×source, device×entry, source×path, source×entry, entry×exit), unknown values, two different paths; funnel and props;
  - monthly rollups, including partial months (monthly − excluded days);
  - dropping every hot sessions partition after closing, then the raw tier again with all filter combos;
  - idempotent recompute; rollup-stream == oracle; verify passes on oracle-rendered truth and fails on a perturbed truth;
  - **`TestGenTinyVerify`**: gen's tiny fixture must pass verify (raw included) for both SQL and stream rollups.

## Measurements

The reports `proto-initial-20261007.md` and `proto-opt-20261007.md` were removed 2026-10-10 (in git at `7e7294d`); headline numbers are in progress.md (all "dev" numbers: shared box, another agent loading and querying incumbents, load average 5–14, heavy foreign disk reads).

Initial pass (Tier S 30 d): C1 0 mismatches; COPY 456K rows/s (1 writer) to 1.07M (4); `load` 516K ev/s; HTTP 10K ev/s at p99 10.9 ms; idle RSS 19 MB; S3 4.9–8.3 s; R2 184–190 MB.

Optimisation pass, biggest site, p95:

| Line | Target | Tier S 90 d | Tier M 14 d |
|---|---|---|---|
| S2 rollups | < 300 ms | 30 d 67 ms, 90 d 139 ms | 14 d 253 ms; 365 d synthetic history 133 ms (whole months), 584 ms (350 d) |
| S3 filtered, exact (country / +source / +path) | < 1 s S, < 3 s M | 1.31 / 0.88 / 1.06 s (runs spread 0.88–1.79 s) | 1.87 / 1.07 / 1.27 s |
| S3 before (hot tables, original queries, same box) | | 56.8 / 34.9 / 53.2 s | |
| S3 opt-in approximate, 1/16 sample | | 0.31 / 0.24 / 0.27 s, top-10 visitors median error 0.1–0.9% | |
| S4 funnel | < 2 s (S 30 d) | 30 d 0.40 s (was 10.2 s) | 14 d 1.64 s (hot 16.0 s) |
| F4 props | < 1 s (S 30 d) | 30 d 66 / 73 ms (was 9.2 / 4.8 s) | 14 d 182 / 230 ms |
| R2 | < 150 MB | 110.6 MB at 551K live sessions (was 190.6) | |
| C1 | exact | 0 mismatches, 432,500 checks | 0 mismatches, 67,700 checks |
| R3 | ≤ 300 B/event | | ≈ 236 B/event (events 148, closed sessions 53, custom 18, rollups 6, cube 4, hot sessions 7) |

On this box every read goes through PG 18's 3 io workers with 128 MB shared_buffers; they, not the query CPU, bound the Tier S S3 dashboard (~1.5 GB read per dashboard).

### Dyadic day blocks (2026-10-07)

Level 0 reuses the existing daily dimension rollup; levels 1–9 are aligned to days since the Unix epoch and maintained transactionally when a day closes or `rollup-stream` replaces it. A completion table distinguishes an empty completed block from one not built. Against the restored synthetic 365-day Tier-M history (30 runs), monthly-plus-dyadic beat pure dyadic: **224.4/143.7 ms p95** versus **481.6/401.5 ms** for misaligned 350d/aligned 365d, so the hybrid is the default. The final hybrid run was **221.979/128.210 ms**, versus the pre-change **584.317/132.522 ms** baseline. Full backfill took 11m45s and added **24,911,017 metric rows + 71,800 completion rows, 5,706,702,848 bytes (5.44 GiB)** including indexes. Result: `../results/codex-dyadic-20261007-095644.json`.

## Risks for gate step 5

1. **S3 Tier S < 1 s, exact, is not yet shown** (1.06–1.31 s p95 for 2 of 3 cases on the shared box). Re-measure on the quiet box with the tuned profile (shared_buffers ≥ 2 GB keeps site 1's 1.5 GB of closed rows in memory). Next exact steps if it still misses: combo ids for the visitor dims and UTM columns (−20 B/row), a separate physical home for the biggest sites' rows. The opt-in sampling mode (0.24–0.31 s, ≤ 1% median error) is the fallback: a product decision.
2. ~~**S2 over an arbitrary 365-day range at Tier M**~~ resolved by monthly-plus-dyadic blocks: final 350d/365d p95 221.979/128.210 ms; both under 300 ms.
3. **S4 at Tier M over 30 days** is ~3.5 s (*estimate*; 1.64 s for 14 days).
4. **Raw-event retention trade-off**: O5 now proves seven-day raw storage plateaus via whole daily-partition drops while closed tables and rollups remain. Total database size still grows with the deliberately retained closed tier; re-deriving it from dropped raw events is impossible.
5. Closed-tier rebuilds (DELETE + INSERT) bloat; use partition swaps if rebuilds become routine.
6. All numbers are dev numbers from a box shared with other agents, client and server co-located.

## MUST-line prep run (2026-10-07)

All write tests used disposable schemas because the `panel` role lacks `CREATEDB`; no existing rows in the large `panel` database were changed. Scripts clean their schemas on exit and never restart or reconfigure PostgreSQL.

- O2: three schema-from-nothing runs, 0.517/0.514/0.506 s; median 0.514 s.
- O3: one-command forward migration 7→8; 20 events and 11 sessions survived.
- O4: schema-scoped custom `pg_dump`/`pg_restore`; 32,991 events; `proto verify` 0/14,740 mismatches.
- O5: 21 tiny days, raw retention 7 days; 8 UTC partitions at the end (timezone spillover), steady raw size 11.35–11.59 MB (2.17% range); all 2,100 site-day rollups retained.
- R5/P3: binary 12,677,385 bytes stripped; tracker 723 bytes minified and 410 bytes gzip; no cookie or storage APIs.
- P1: one real-HTTP plus one `proto load` event; 0 matches for four generated IP/UA secrets in schema dump and logs.
- P2/P4: random-source/error, 48-hour cutoff, cross-site hash, and hostile payload tests pass.
- C2: after `kill -9` and SIGTERM restarts, 4 events = 4 pageviews in 1 visit; two startups rehydrated the open session.
