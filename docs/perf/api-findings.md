# API performance findings

Baseline frozen 2026-10-12 at commit `d37dd61f` (main). Rig: this dev machine (12 vCPU, 17 GB; core D15), Go go1.27.2, Linux 6.12.74+deb13+1-amd64. PostgreSQL version: recorded by the benchmark harness (plan Task 4). Numbers compare runs on this machine only; they are not publishable.

Plan: `docs/superpowers/plans/2026-10-12-api-tests-and-performance.md`.
Prior evidence: `docs/perf/2026-10-09-query-audit.md` (11 findings at `93c5d59`; re-verified in Task 5).

## Findings

None recorded yet. Task 3 adds production bugs found by the endpoint matrix tests; Task 5 adds ranked slow routes.
