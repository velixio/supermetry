# Benchmark contract (go/no-go gate steps 4–5)

The shared spec for every agent working under `bench/`. Thresholds and the rig are in [../scorecard.md](../scorecard.md). Hard rules are in [../CLAUDE.md](../CLAUDE.md): never touch `knowledge/`, never copy AGPL code, and be test-first for core logic. This is **throwaway benchmark code**, not the product.

## Layout and ownership

| Dir | Owner | What |
|---|---|---|
| `bench/rig/` | rig agent | Docker; Plausible CE, Umami and GoatCounter setup; `up.sh`/`down.sh`/`status.sh`; PG tuning profile |
| `bench/gen/` | gen agent | Go module `supermetry/bench/gen`: deterministic synthetic data generator plus ground truth |
| `bench/proto/` | proto agent | Go module `supermetry/bench/proto`: the Supermetry prototype (schema, ingest, rollups, query set) |
| `bench/load/` | phase B | Loaders for incumbents, query harness, HTTP load tester |
| `bench/results/` | everyone | Measured numbers, as markdown/JSON, with date, commit and rig state |

Agents only write inside their own dir, plus `bench/results/`. Each keeps a `NOTES.md` in its dir.

## Ports and databases

- Plausible CE: `:8000`, using its own compose PG + ClickHouse.
- Umami: `:3030`. GoatCounter: `:8083`. Supermetry prototype: `:8090`. These moved from :3000/:8081 because another project's dev servers hold those ports; see `bench/rig/rig.local.conf`.
- The local PG 18 server on `:5432` (stock config for now) hosts the databases `umami`, `goatcounter` and `supermetry`, each with a role of the same name. Create them with `sudo -u postgres`.
- **Do not restart or reconfigure the local PG.** The rig agent only *prepares* `bench/rig/pg-tuning.conf` plus an apply script; it is applied once, before measurement runs.

## Canonical event stream (NDJSON)

One JSON object per line, **globally sorted by `ts` ascending**:

```json
{"ts":"2026-07-03T00:00:01.123Z","site":1,"host":"site1.test","path":"/blog/x","ref":"https://www.google.com/","utm_source":"","utm_medium":"","utm_campaign":"","name":"pageview","props":{},"ip":"10.1.2.3","ua":"Mozilla/5.0 …","screen_w":1440,"country":"IN","browser":"Chrome","os":"Android","device":"mobile","vk":"…","sk":"…"}
```

- `name` is `"pageview"` or a custom event name. `props` is a string→string map, non-empty only for custom events, with at most 3 keys.
- `ip` and `ua` are realistic but synthetic. `country`, `browser`, `os` and `device` are the parsed ground truth, so direct loaders don't need GeoIP or UA parsing.
- `vk` and `sk` are the **ground-truth** visitor and session keys:
  - The systems under test must **never** use them for counting. Supermetry derives IDs from `ip`+`ua`+site, exactly as in production.
  - The only exception is incumbent loaders that bypass ingest and write storage directly: they may use `sk` to build session rows.
- The generator also writes `sites.json`: `[{id, host, timezone, share}]`. The 100 sites include `UTC`, `Europe/Berlin`, `America/New_York`, `Asia/Kolkata` (+5:30) and `Asia/Kathmandu` (+5:45).

## Truth semantics

The generator **guarantees** these, so the truth is unambiguous:
- **Visitor**: a unique (`ip`, `ua`) per site per **site-local day**. A visitor's `ip`+`ua` is stable within a day.
- **Session**: one visitor's events, where every intra-session gap is ≤ 25 min and inter-session gaps are ≥ 35 min, so a 30-minute idle timeout is exact. Sessions never cross site-local midnight.
- **Pageviews**: the count of `name="pageview"`.
- **Bounce**: a session with exactly 1 pageview and 0 custom events.
- **Duration**: last `ts` − first `ts` of the session, which is 0 for a single event.
- **Entry/exit page**: the path of the session's first and last pageview.
- **Funnel F**: steps `/` → `/pricing` → custom event `signup`, in order within one session. Count the sessions reaching each step.
- **Prop**: custom event `signup` has prop `plan` ∈ {free, pro, team}; count by value.

## Ground-truth file (`truth.json`)

Per (site, local_date), output:
- `visitors`, `visits`, `pageviews`, `bounces`, `duration_sum_s`;
- the top 50 values (by visitors) with visitors and pageviews for each dimension `path`, `country`, `source` (referrer host, or utm_source when set), `browser`, `os`, `device`, `entry`, `exit`;
- funnel F step counts;
- `signup.plan` counts.

## Tiers

| Tier | Volume | Notes |
|---|---|---|
| S | 1M events/day × 90 days, ending 2026-09-30 | 100 sites, Zipf traffic; the biggest site is about 30% |
| M | 10M events/day × 14 days | the raw tier |
| M-history | 10M/day × 365 days | **Supermetry rollups only**: streamed through the rollup builder without being stored raw. Incumbents are n/a, because they can't store 3.65B rows on this disk |

Shared shape: 2.5 pageviews per session on average, about 55% bounce, 10% custom events, 20K distinct paths on the biggest site, and a daily traffic cycle.

## Interfaces between parts

- `gen --tier S --days 90 --seed 42 --out - --sites sites.json --truth truth.json` writes NDJSON to stdout (`--zstd` optional).
- The prototype bulk-loads by reading NDJSON on stdin: `proto load < events.ndjson`, running the same sessionization and hashing as its HTTP ingest. It also serves HTTP ingest on `:8090`.
- There are no Go imports between modules. The NDJSON format is the contract.
