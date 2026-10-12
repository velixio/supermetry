# API performance findings

Baseline frozen 2026-10-12 at commit `d37dd61f` (main). Rig: this dev machine (12 vCPU, 17 GB; core D15), Go go1.27.2, Linux 6.12.74+deb13+1-amd64. PostgreSQL version: recorded by the benchmark harness (plan Task 4). Numbers compare runs on this machine only; they are not publishable.

Plan: `docs/superpowers/plans/2026-10-12-api-tests-and-performance.md`.
Prior evidence: `docs/perf/2026-10-09-query-audit.md` (11 findings at `93c5d59`; re-verified in Task 5).

## Findings

### Route coverage baseline (2026-10-12, main `f3521a52`)

187 registered routes, 82 in OpenAPI, 151 hit by at least one test (36 never hit). Details: `docs/api/inventory.md`. Untested: `api/public/v1` 16 of 48, `api/private/sites` 5 of 43, plus one each for stats (3), realtime (2), funnels, health, heatmaps, journeys, query, retention, sessions, vitals, replay chunk/start.

### Open bugs and test-environment issues

| # | Where | What | Status |
|---|---|---|---|
| B1 | `test/platform/flow_test.go:92` `TestPlatformFlow` | `share pageviews = 0` after creating a share link; fails every time, also on `dd83f330` (before the API-plan merges). Likely from the share-link work (migration 0049 share config, `ab2a509c`, `4b935ae3`). Not caused by this plan. | open, owner: shares work |
| E1 | `/tmp` | Go link steps fail with "no space left on device" when `/tmp` (16 GB tmpfs, shared with other projects) fills. Run Go with `GOTMPDIR`/`TMPDIR` on the main disk (`/home/user/.cache/go-build/tmp-*`). | workaround |
| E2 | `test/golden` `TestGracefulShutdownLosesNothing` | "supermetry did not become ready" under full-suite load; passes alone in 20 s. | flaky under load |

### Tier S baseline (2026-10-12, commit `bf79184c`, `make bench`)

198 request sets across 184 routes, 336,336 events, 30 days, seed 42, PostgreSQL 18.6, 37 s total. Concurrency 1. Nothing exceeds the 200 ms budget: worst p95 is `GET /api/public/v1/stats/breakdown` at 47 ms.

| Lead | Evidence | Next step |
|---|---|---|
| Many queries per request | `POST /api/public/v1/funnels/query` makes **45** DB queries per request (7.2 ms DB time); `stats/breakdown`, `stats/timeseries`, `query` 13-14; `stats/summary` 5-10. 9 request sets exceed 10 queries. | Task 5: read the query loop behind each; batch or join where the count scales with input. |
| Cold first request | Stats and profile routes: cold 100-141 ms versus warm 2-27 ms (21 request sets are cold > 5x p50). | Task 5: check plan caching and first-use initialisation; measure with a warmed pool. |
| Tier S too small | 336K events do not reproduce the audit's 421 ms Web Vitals filter. | Run `make bench-full` (tier M) before ranking. |

Nine request sets (`defaults only`) return HTTP 500 because the compact bench environment does not install optional services (for example `GET /api/private/erasures/{erasure}`); that is a harness gap, not a product bug.

### Tooling fixes

- 2026-10-12 `8f9c1933`: routers mounted behind request-cloning middleware were recorded as their catch-all mount pattern (35 routes hidden in the coverage inventory). The recorder now records the innermost pattern.

Task 3 adds production bugs found by the endpoint matrix tests; Task 5 adds ranked slow routes.
