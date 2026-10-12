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

Task 3 adds production bugs found by the endpoint matrix tests; Task 5 adds ranked slow routes.
