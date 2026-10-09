# bench/gen: notes

Deterministic synthetic web-analytics event generator with ground truth, per [../CONTRACT.md](../CONTRACT.md).
This is throwaway benchmark code. Go 1.26, stdlib plus `klauspost/compress/zstd`.

## Usage

```sh
go build -o gen .
./gen --tier S --days 90 --seed 42 --out - --sites sites.json --truth truth.json [--zstd]
./gen --tier tiny --out testdata/tiny.ndjson --sites testdata/tiny.sites.json --truth testdata/tiny.truth.json   # = go generate
./gen verify --sites sites.json --truth truth.json [--zstd] < events.ndjson     # independent recount, exit 1 on any diff
```

| Tier | Events/day | Default days | Default start (ends 2026-09-30) |
|---|---|---|---|
| `tiny` | 10K | 3 | 2026-09-28 |
| `S` | 1M | 90 | 2026-07-03 |
| `M` | 10M | 14 | 2026-09-17 |
| `M-history` | 10M | 365 | 2025-10-01 |

Other flags: `--start YYYY-MM-DD`, `--workers N` (output never depends on it), `--events-per-day N` (experiments only), `--quiet`.
The summary on stderr gives counts, rate, bytes and max RSS. The CLI sets a 1 GiB soft memory limit unless `GOMEMLIMIT` is set.

## Layout

- `main.go`: the CLI (generate, `verify`).
- `internal/synth/`: the generator. `model.go` holds every tunable and table; `generator.go` sites, identities, volume; `unit.go` the per-(site, day) simulation; `truth.go` the generator's own accounting; `format.go` the NDJSON; `run.go` the pipeline.
- `internal/recount/`: an **independent** recount from the NDJSON alone (encoding/json, ip+ua visitors, 30-minute-timeout sessions). It imports nothing from `synth`. It also checks every stream invariant. `gen verify` uses it.
- `testdata/`: `tiny.ndjson` (33K events, 16 MB), `tiny.truth.json`, `tiny.sites.json`, for other agents. `TestFixturesUpToDate` fails if they go stale.

## Design

**Unit = (site, site-local date).** Each site gets exactly `--days` complete local days, so every truth row is a full day. The stream therefore spans more than `[start, end]` in UTC: from about 13 h before (Pacific/Auckland) to 7 h after (America/Los_Angeles).

**Keyed randomness.** Every decision draws from a splitmix64 stream keyed by `(seed, purpose, site, date, visitor index)`, never by generation order. So:
- output is byte-identical for the same flags, at any `--workers`;
- a day's events depend only on (seed, tier volume, site, date). A later `--start` reproduces the same days (`TestLaterStartIsSuffix`), and **tier M is exactly the last 14 days of M-history**.

Integer weights and piecewise-uniform buckets are used throughout, with no `exp`/`log` in the hot path. Time zones come from the embedded `time/tzdata`.

**Volume.** Visitors per (site, day) = events/day × Zipf share (s = 1.25, so the biggest site is 30.0%) × weekday factor (Sun 0.88 … Tue/Wed 1.10, mean 1) × ±8% day noise ÷ 3.302 events per visitor. The divisor is measured, and `TestEventsPerVisitorCalibration` keeps it honest.

**Identity and returning visitors (closed form, no state).**
- `R_d = min(30% of N_{d-1}, N_d / 2)` visitors of day d are returners.
- Visitor `i < R_d` of day d *is* visitor `R_{d-1} + i` of day d−1: same origin, so the same ip, ua, country, screen and `vk`.
- Only first-time visitors return, so origins are at most one day old. A visitor can therefore be computed for any day alone.
- A person's attributes derive from its origin `(day, index)`. The IP is a keyed bijection of `(origin day mod 4, index)` into 64 public /8s, so everyone who can appear on a site on days d and d+1 has a distinct IP. That makes (ip, ua) unique per site-day **by construction**, not by luck.
- `vk` = bijective mix of (site, origin day, index). It is stable across days for a returner; count distinct `vk` *per day* for visitors.
- `sk` = bijective mix of (site, day, visitor, session ordinal) with the top bit set. Both are unique by construction.

**Guarantees, enforced by construction:**
- Intra-session gaps are 1 s … 25 min (bucketed dwell times). Inter-session gaps are 35 min + extra.
- Every event of a visitor-day lies in `[lo, hi)` of the local day, and events at or after `hi` are dropped (that is how a late session ends).
- `lo = 00:20` for returners and `hi = 24:00 − 20 min` for visitors who return tomorrow. A returning (ip, ua) is thus ≥ 40 min apart across midnight, so even a 30-minute timeout that ignores dates never merges days.
- Timestamps are strictly increasing within a visitor. Global ties between different visitors are broken by site id, then generation order.

**Pipeline** (`run.go`). Gen workers simulate units into compact arrays: 16 B per event, 8 B per session, 16 B per visitor. Each unit is radix-sorted (stable) by local offset. The main goroutine then works through 2-hour UTC windows:
1. waits for the units overlapping the window (generation runs 12 h ahead);
2. k-way merges them by (ts, site);
3. sends 4096-event batches to format workers, which zstd-compress each batch into its own frame (concatenated frames are a valid zstd stream);
4. an ordered writer writes the batches and releases units after their last window.

Memory is about 1.5 days of compact events plus in-flight buffers. Truth rows are serialized by the gen worker and streamed out in (date, site) order.

## Behaviour model (measured on 2 days of tier S, `TestDistributionsTierS`)

| Target | Measured |
|---|---|
| Pageviews/session ≈ 2.5 | 2.497 |
| Bounce ≈ 55% | 55.4% |
| Custom events 10% | 9.96% (signup/download/outbound, 3 props each) |
| Referrers ≈ 40% direct, 30% google | 40.1% / 29.9% of sessions; then bing, ddg, facebook, t.co, linkedin, reddit, HN, github, chatgpt, … plus 40 small blogs |
| UTM ≈ 10% | 10.5% (newsletter/email, google/cpc, facebook, twitter, linkedin, producthunt) |
| Biggest site ≈ 30% | 30.6% of events (share 0.3001) |
| Next-day return ≈ 30% | 30.0% of (site, ip, ua) |
| Mobile / desktop / tablet | 55% / 42% / 3%; screen widths per device and iOS/other (mobile < 576, tablet 768–962, desktop ≥ 1280) |
| Countries | 50 weighted (US 22%, IN 12%, DE 7%, GB 6%, BR 5%, …), plus 30% home country for sites with one (NY→US, Berlin→DE, Kolkata→IN, Kathmandu→NP, …) |
| 20K distinct paths on the biggest site | vocabulary 20,000 (Zipf s = 1): 17.8K seen in one S day, 19,999 in a week; smallest site 267 (177/day) |
| Funnel F | step2/step1 = 16%, step3/step2 = 26% (`/` → `/pricing` 22%; custom event after `/pricing` 30%, 75% of them signup) |
| signup.plan | free 60%, pro 30%, team 10% |
| Daily cycle | session starts by site-local hour, minimum around 04:00 and peak 11:00–14:00 |

Sites: `site{N}.test`. Sites 1–5 are America/New_York, Europe/Berlin, Asia/Kolkata, UTC and Asia/Kathmandu. Sites 6–100 cycle through those plus London, Los Angeles, São Paulo, Tokyo, Sydney, Paris, Chicago, Singapore, Madrid and Auckland. The DST changes inside M-history are covered by `TestTruthAcrossDSTChanges`.

## truth.json

The file is one JSON object, with one row per line:

```json
{"generator":"supermetry/bench/gen","tier":"S","seed":42,"start":"2026-07-03","days":90,"events_per_day":1000000,"sites":100,
 "dims":[…],"top_n":50,"funnel_f_steps":["/","/pricing","signup"],"rows":[
{"site":1,"date":"2026-07-03","visitors":…,"visits":…,"pageviews":…,"bounces":…,"duration_sum_s":200633.678,
 "top":{"path":[{"value":"/","visitors":453,"pageviews":552},…],"country":[…],"source":[…],"browser":[…],"os":[…],"device":[…],"entry":[…],"exit":[…]},
 "funnel_f":[n1,n2,n3],"signup_plan":{"free":…,"pro":…,"team":…}},
…
],"events":N}
```

Rows are ordered by (date, site). `date` is the site-local date.

## Interpretations of CONTRACT (simplest consistent reading; please flag disagreements)

1. **Breakdown semantics** = "filter by dimension = value, then count":
   - `path`: visitors with ≥ 1 pageview on the path, and pageviews of it. Custom events don't count.
   - `country`/`browser`/`os`/`device`: these are visitor attributes. Visitors, and those visitors' pageviews.
   - `source`/`entry`/`exit`: these are session attributes. Visitors with ≥ 1 such session, and the pageviews of those sessions.
2. **Top 50 ordering**: visitors desc, then pageviews desc, then value asc. Values tied at the 50th place may legitimately differ in other systems.
3. **source** comes from the session's first event: `utm_source` if set, else the referrer URL's host **verbatim** (`www.google.com`, `l.facebook.com`, `t.co`; no `www.` stripping and no name mapping), else `"(direct)"`. Plausible's "Google" / "Direct / None" naming must be mapped by the harness.
4. **ref/utm on later events**: only the landing page carries the external referrer and UTM. Custom events on that page carry them too, like a real tracker would. Later pageviews carry an internal referrer `https://siteN.test/<previous path>`, as browsers send.
5. **Funnel F**: greedy in-order match within a session. Steps 1–2 are pageviews of `/` and `/pricing`; step 3 is a `signup` event after them. `funnel_f[i]` = sessions reaching step i+1.
6. **signup.plan counts are event counts**, not visitors. All three keys are always present.
7. **duration_sum_s** is exact at ms precision, printed with 3 decimals. Systems that store whole-second timestamps will be off by up to 1 s per session (the scorecard allows ±1%).
8. **Visitors** are per site-local day; there is no "new vs returning" in the truth. `vk` is person-stable across days, which is useful for Umami-style session rows.
9. **Props**: the scorecard says "3 props each", the CONTRACT says "at most 3", so every custom event has exactly 3. Keys are sorted: signup {billing, method, plan}, download {file, position, type}, outbound {domain, position, url}.
10. **IPs** are public-range but synthetic; a GeoIP lookup will **not** match `country`. Any HTTP-ingest test that relies on an incumbent's GeoIP should compare country breakdowns as INFO only.

## Measured performance

Measured on this rig (12 vCPU, 17 GB, 2026-10-07). "pipe" means `gen --out - | wc -c`, which is what loaders see.

| Run | Events | Sink | Wall | Rate | Max RSS | Output |
|---|---|---|---|---|---|---|
| S, 90 days (full tier) | 90.2M | pipe, raw | 23.9 s | 3.78M ev/s | 0.40 GB | 44.1 GB (489 B/event) |
| S, 90 days (full tier) | 90.2M | pipe, `--zstd` | 12.7 s | 7.12M ev/s | 0.75 GB | 4.25 GB (47 B/event) |
| M, 14 days (full tier) | 140.3M | pipe, raw | 30.9 s | 4.54M ev/s | 1.04 GB | 68.7 GB (489 B/event) |
| M, 14 days (full tier) | 140.3M | pipe, `--zstd` | 26.6 s | 5.28M ev/s | 0.90 GB | 8.24 GB (59 B/event) |
| M, 4 days | 41.8M | `/dev/null` (generation + formatting only) | 2.9 s | 14.4M ev/s | 1.19 GB (no memory limit) | |
| M-history, extrapolated | ≈ 3.66B | pipe, raw | ≈ 13.5 min | | ≈ 1 GB | ≈ 1.8 TB raw, ≈ 215 GB zstd |

- **Per 1M events: 489 MB raw NDJSON, 47–59 MB with `--zstd`** (level 3, one frame per 4,096 events).
- Raw output is bound by the pipe and the consumer; generation alone runs at about 14M events/s.
- truth.json: S 133 MB (9,000 rows, about 15 KB each), M 22 MB. M-history is about 570 MB.
- `gen verify` is single-threaded at about 80–130K events/s, with about 2.4 GB RSS for one M day (string maps). It reported 0 violations and 0 diffs on tiny, S × 3 days (3.3M events) and M × 1 day (11.1M events).
- The scratch directory is tmpfs, so disk write speed was not measured. Expect 489 B/event at whatever the disk sustains.

## Tests (`go test ./...`, about 15 s; `-short` skips the tier-S sample)

- `TestTruthEqualsIndependentRecount`: tiny truth must equal the `recount` package's recount exactly, with zero invariant violations (every line's key order is checked). Mutations were tried: bounce ignoring custom events, a 27-min page gap, no midnight guard, a 31-min inter-session gap, and a funnel skipping `/pricing`. Each made this test fail.
- `TestTruthAcrossDSTChanges`: the same check over EU/US/Sydney/Auckland DST transitions.
- `TestDeterministicAcrossWorkerCounts` (1, 4 and 11 workers, plus a different seed); `TestZstdIsDeterministicAndRoundTrips`; `TestLaterStartIsSuffix`; `TestFixturesUpToDate`.
- `TestDistributionsTierS`: the realism table above, within tolerances.
- `internal/recount`: hand-computed fixtures for every metric, plus violation detection.
- `internal/synth`: site shares and zones, path vocabulary, IP distinctness across adjacent days, vk/sk distinctness, returner ranges, stable radix sort, timestamp formatting, DST day lengths and +05:45 midnight, and the events/visitor calibration.
