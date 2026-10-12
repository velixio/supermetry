# API test coverage, benchmark and performance plan

> **For agentic workers:** Execution method is fixed by the user: **Codex** does the implementation (one job per task, run with `scripts/codex-job.sh`), Claude writes/extracts specs, verifies and analyses. Steps use checkbox syntax for tracking.

**Goal:** Every HTTP API route has an automated test, every route has a measured latency under a repeatable benchmark, and the slowest responses are found, explained with evidence and fixed.

**Architecture:** (1) A runtime route-hit recorder plus a generated inventory give exact route/OpenAPI/test coverage. (2) Table-driven endpoint matrix tests (auth, tenant isolation, validation, pagination) per route group. (3) A benchmark harness drives each route on generated datasets and records latency, response size and DB queries per request. (4) Slow routes get EXPLAIN and pprof evidence, then one TDD fix each. (5) A quick benchmark mode is a regression gate.

**Tech Stack:** Go (`net/http` ServeMux patterns, `pgx`, `slog`), PostgreSQL, existing `internal/testpg`, `tools/gen` + `internal/synth` generator, `tools/bench/load`, OpenAPI at `internal/api/openapi.yaml`.

**Scope ("all the projects"):** core `supermetry` now. `supermetry-site` is a static Go file server: its own tests come with site job 01, plus a small static-serving benchmark in Task 7. `supermetry-cloud` has no code yet: it reuses this harness once its admin/billing API exists.

**Spec:** this document; evidence base is `docs/perf/2026-10-09-query-audit.md` (11 findings at commit `93c5d59`, **line numbers drift, re-verify each**).

## Global Constraints

- Rig = this machine (12 vCPU, 17 GB; core D15). Numbers are for comparison between runs, **not publishable** until a real-VPS re-run.
- Hard rules from `CLAUDE.md` stand: no DB read on the ingest hot path, no ClickHouse/Kafka/Node, forward-only migrations, retention by `DROP PARTITION`, rollups idempotent, no raw IP/UA stored, `knowledge/` read-only.
- Tests: Go `testing`, table-driven, `-race` clean; DB tests use `SUPERMETRY_TEST_DSN` through `internal/testpg` (never print the DSN).
- Metric **values** stay covered by the existing hand-computed golden tests. API tests check behaviour (status, auth, shape, errors), not recomputed metrics.
- Errors are RFC 9457 `application/problem+json`, dates RFC 3339, time zones IANA (core D24).
- A test that exposes a production bug is recorded in `docs/perf/api-findings.md` and fixed in a **separate commit** with its own message; never silently weaken a test.
- Commits: velixio identity, no trailers, no AI mentions, email scan before each commit; 4–8 small commits per task; Codex does **not** push.
- Max **2 Codex jobs in parallel** that touch the DB until the template-database speed-up (Task 0, step 3) lands: earlier runs timed out with 3–4 sharing the dev DB.

## Review Focus (failure modes the tasks must pin with tests)

1. **Cross-tenant access (IDOR):** every `{id}` route must return 403/404 for an object of another org, never 200. (Task 3, per group.)
2. **Unbounded inputs:** `limit=1000000`, 10-year ranges, 10,000-value filter lists, huge `page`/cursor: expected a 4xx or a clamped result within the latency budget, never a multi-second query. (Task 3, Task 5.)
3. **Cold vs warm numbers:** a benchmark that only reports warm cache hides real latency; each route reports first-hit and steady-state separately. (Task 4.)
4. **Time-zone bucketing:** a site in +05:30/+05:45 returns correct day boundaries on the stats endpoints. (Task 3, `public/v1`.)
5. **Empty, deleted and restorable sites:** zero-event site, site in 7-day restorable deletion (core D62), demo site (24 h life): defined status codes, no 500s. (Task 3.)

(Pagination cursor stability under concurrent inserts is a sixth candidate; add it to Task 3's list only if the inventory shows cursor endpoints beyond `events`.)

---

## File map

| Path | Responsibility |
|---|---|
| `internal/platform/http/routehits_test.go` (+ small non-test hook) | records matched route patterns during `go test` |
| `tools/apiinv/` | builds `docs/api/inventory.json` + `.md` from recorder output + OpenAPI |
| `internal/platform/http/openapi_drift_test.go` | fails on routes missing from OpenAPI or the reverse |
| `internal/testapi/` | shared test server: seeded org/site/key/session helpers, request builder |
| `internal/platform/http/*_matrix_test.go`, `internal/api/*_matrix_test.go` | per-group endpoint matrices |
| `tools/bench/api/` | per-route benchmark runner, dataset loader, result JSON |
| `docs/perf/api-baseline-YYYY-MM-DD.md` | committed baseline table (generated) |
| `docs/perf/api-findings.md` | ranked slow routes with evidence, status per finding |
| `scripts/api-bench.sh`, `Makefile` target `api-bench` | quick and full runs |

---

### Task 0: Prerequisites (Claude, ~10 minutes, no Codex)

- [ ] **Step 1: Restore project skills.** `ls .claude/skills/` failed earlier in this checkout (CLAUDE.md says they are installed; `skills-lock.json` pins them). Restore with `npx skills experimental_install` (or `npx skills add <pkg> -a claude-code -y` for `golang-testing`, `golang-performance`, `golang-concurrency`, `supabase-postgres-best-practices`). Scan each new SKILL.md for risky instructions.
- [ ] **Step 2: Search for extra skills** with the `find-skills` skill (`npx skills find api load testing`, `npx skills find go benchmark`); mention only 1K+ installs from reputable owners and ask before adding.
- [ ] **Step 3: Test speed (pending user go-ahead from the 2026-10-11 note).** Template-database copy in `internal/testpg` and the Go test cache in the per-task runner gate. Measured basis: one full migration run per DB test costs 1.2–1.4 s × 206 setups. This is what makes the matrix tests in Task 3 cheap.
- [ ] **Step 4: Freeze a baseline commit** and record `git rev-parse --short HEAD`, Go and Postgres versions, machine in `docs/perf/api-findings.md` header.

### Task 1: Route inventory and exact coverage

**Files:** Create `tools/apiinv/main.go`, `docs/api/inventory.{json,md}` (generated). Modify: the router setup file that registers routes (find with `grep -rn "HandleFunc\|Handle(" internal/platform/http`).

**Interfaces:**
- Produces: `inventory.json` = list of `{method, pattern, handler, auth: "public-key|session|none|instance-admin", in_openapi: bool, hit_by_tests: bool, test_files: []string}`. Later tasks read it to find gaps.

- [ ] **Step 1:** Add a recorder middleware compiled only into tests (or enabled by `SUPERMETRY_ROUTE_HITS=<file>`): after routing, append `r.Pattern` (Go 1.23+ `http.Request.Pattern`) to the file. Default off, zero cost in production.
- [ ] **Step 2:** `tools/apiinv` collects the registered patterns (parse the registration calls with `go/ast`, not regex), runs `go test ./...` with the env var set, merges hits and the OpenAPI operations, writes JSON and a Markdown table sorted by group.
- [ ] **Step 3:** Acceptance: `go run ./tools/apiinv` produces the files; the Markdown lists total routes, routes documented in OpenAPI, routes hit by at least one test, and the unhit/undocumented lists. (Rough grep baseline, to be replaced by exact numbers: ~140 route registrations vs 81 OpenAPI operations; largest groups `api/private/sites` ~38, `api/public/v1` ~29, `api/private/orgs` ~22, `api/private/me` ~16, `api/private/auth` 8.)
- [ ] **Step 4:** Commit: recorder; tool; generated inventory.

### Task 2: OpenAPI drift guard

**Files:** Create `internal/platform/http/openapi_drift_test.go`.
**Consumes:** Task 1 registration list. **Produces:** a test that fails when a public route is undocumented or an OpenAPI operation has no route.

- [ ] **Step 1:** Write the test first against the current tree; it will list drift. Private (session-only dashboard) routes may be excluded only through an explicit allowlist file `internal/platform/http/testdata/openapi_private_allowlist.txt`, one pattern per line with a reason; the public `api/public/v1` group has **no** allowlist.
- [ ] **Step 2:** For each documented operation, validate the declared success status and `Content-Type` against a real response from Task 3's seeded server (add in Task 3, reuse the helper).
- [ ] **Step 3:** Either document the missing public operations in `openapi.yaml` or add them to the allowlist with a reason. Regenerate the TS client (`web/packages/api-client`) only if the spec changed, and run the web typecheck.
- [ ] **Step 4:** Commit.

### Task 3: Endpoint matrix tests (parallelisable by group, one owner each)

**Files per job:** Create `internal/testapi/*.go` (job 3a only), then `<group>_matrix_test.go` files. Groups (disjoint files): **3a** helpers + `api/public/v1` (29); **3b** `api/private/sites` + goals/funnels/segments/profiles/share; **3c** `api/private/orgs`, `me`, `auth`, `instance`. Each job reads `docs/api/inventory.md` for its routes.

**Interfaces:**
- `testapi.New(t) *Env` with `Env.Org(t) Org`, `Env.Site(t, org) Site`, `Env.APIKey(t, org, scopes ...string) string`, `Env.Session(t, user) *http.Cookie`, `Env.Do(t, Req) Resp`, and a `Req`/`Resp` that exposes status, headers, decoded problem+json.

Case table (the pattern every route must use):

```go
type apiCase struct {
	Name   string
	Method string
	Path   string // may contain {site}, {org}, {id}; expanded by the helper
	Auth   authKind // none, session, apiKey(scope), otherOrgSession, readOnlyKey
	Body   any
	Want   int    // status
	WantProblem string // RFC 9457 "type" suffix, when Want >= 400
}
```

Minimum rows per route: no credentials → 401; wrong scope → 403; **other org's object → 403/404** (Review Focus 1); malformed id/body → 400/422 with problem+json; valid request → documented success status and `Content-Type`; list routes: default limit, max limit clamp, cursor next/prev, empty result; read routes: `RateLimit-*` headers on public/v1, 429 + `Retry-After` when exceeded.
Extra rows from Review Focus: unbounded `limit`/range (2), +05:30 and +05:45 sites on stats endpoints (4), zero-event site, restorable-deleted site, demo site (5).

- [ ] **Step 1 (per job):** write the matrix for its group; run; every failure is either a test bug (fix the test) or a production bug (add a line to `docs/perf/api-findings.md`, mark the case `t.Skip("bug: <id>")` only if the user approves, otherwise leave it failing and list it in the summary).
- [ ] **Step 2:** `go test -race ./internal/... -run Matrix` passes (apart from listed bugs); `go vet ./...`; gofmt clean.
- [ ] **Step 3:** Re-run `go run ./tools/apiinv`; acceptance = **every route in the job's group has `hit_by_tests: true`** and `docs/api/inventory.md` is regenerated.
- [ ] **Step 4:** 4–8 commits per job (helpers, one per sub-group, inventory).

### Task 4: Benchmark harness

**Files:** Create `tools/bench/api/{main.go,dataset.go,report.go}`, `scripts/api-bench.sh`; Modify `Makefile` (target `api-bench`). Codex reads first: `tools/bench/CONTRACT.md`, `tools/bench/load/NOTES.md`, `tools/gen` usage, and extends `tools/bench/load/loadtest` where it already fits instead of duplicating it.

**Interfaces:**
- Consumes: `docs/api/inventory.json` (route list), `internal/testapi` seeding style.
- Produces: `result.json` rows `{route, method, params, dataset, cold_ms, p50_ms, p95_ms, p99_ms, rps, bytes, status, db_queries, db_ms, allocs}` and a Markdown renderer.

- [ ] **Step 1: Datasets.** Tiers from `tools/gen`: **S** 1 site/30 days/~100K events (seconds, regression gate), **M** several sites incl. one big/90 days (D15 limit: 14 days raw), plus a **many-small-sites org** (50 sites) for list endpoints. Same seed every run.
- [ ] **Step 2: Request set.** For each read route, a small list of realistic parameter sets (default range, 7/30/90 days, with 1 and 3 filters, each breakdown dimension, a +05:30 site) and the oversized inputs from Review Focus 2. Write routes (create site/goal/funnel/key) are timed on a fresh org.
- [ ] **Step 3: Measure.** Per request set: 1 cold request (after cache reset), then warm steady-state at concurrency 1 and 8 (N configurable); record p50/p95/p99, bytes, status. **DB queries per request and DB time**: add a request-scoped pgx tracer (test/bench build only) that counts queries; this finds N+1 patterns directly.
- [ ] **Step 4: Profiles.** `--pprof` flag writes CPU and alloc profiles for the run; `--explain` re-runs the worst N queries with `EXPLAIN (ANALYZE, BUFFERS)` into `docs/perf/explain/`.
- [ ] **Step 5: Output.** Compact outputs only: JSON plus a Markdown table sorted by p95 (worst first). **Claude reads the top 20 rows, not raw logs.**
- [ ] **Step 6:** `scripts/api-bench.sh quick` (tier S, under ~2 min) and `full`. Acceptance: `make api-bench-quick` runs twice with p95 spread under 20 % per route (stable enough to gate on); output committed as `docs/perf/api-baseline-<date>.md` (generated, no hand edits).
- [ ] **Step 7:** Commit by layer (dataset, runner, tracer, report, scripts).

### Task 5: Find the slowness (Claude analyses, Codex captures)

- [ ] **Step 1:** Run `full`; Claude reads only the sorted table. Flag a route when any of: p95 above its **budget**, `db_queries > 10` for one request (N+1 candidate), `bytes > 1 MB`, cold/warm ratio above 5×, or p99 more than 5× p50 (lock/GC/cache stampede).
- [ ] **Step 2: Proposed budgets (user to approve):** dashboard reads p95 < 200 ms on tier S and < 500 ms on tier M; list endpoints < 150 ms; writes < 100 ms; public/v1 reads < 300 ms; any route: no unbounded response, errors < 1 %.
- [ ] **Step 3:** For each flagged route Codex job "capture": `--explain` + `--pprof`, output a one-page evidence file per route (query, plan, numbers, top 5 CPU/alloc frames). One shared file, `docs/perf/api-findings.md`, ranked by (p95 × expected traffic), each with `path:line` and the commit.
- [ ] **Step 4:** Re-verify the 11 audit findings from 2026-10-09 against the current commit (`git diff 93c5d59..HEAD -- internal/query internal/api`): mark each *still open / fixed / moved*. Audit items to start from: Web Vitals property filter (421 ms, critical), event explore top-N sort, funnel hot-scan planning time, goals predicate, imported-data `firstNativeDay`, per-request Go orchestration for weekly stats, API-key prefix scan, 1,024-entry closed-range cache, day-close and backfill loops, realtime double scan.
- [ ] **Step 5:** Stop and show the user the ranked list before any fix (one decision: which fixes to do now).

### Task 6: Fixes (one Codex job per finding, TDD, parallel only if files are disjoint)

Candidate improvements to **test, not assume** (hypotheses until Task 5 shows evidence): query shape and indexes (follow `supabase-postgres-best-practices`), prepared statements/plan reuse where planning time dominates, fewer round trips (batch or lateral join instead of loops), `singleflight` for identical concurrent expensive queries, closed-day result cache with a larger bounded size, `ETag`/`Cache-Control` for closed ranges, response compression and smaller payloads, pgx pool sizing, avoiding per-request allocations in JSON encoding, API-key lookup by indexed prefix, clamping oversized inputs with a clear 400.

Per finding:
- [ ] **Step 1:** Failing regression test or benchmark threshold for the route (the budget from Task 5).
- [ ] **Step 2:** Minimal fix. Any schema/index change = forward-only migration, numbered after the latest, with `EXPLAIN` before/after in the commit body.
- [ ] **Step 3:** `go test -race` for the touched packages, golden tests unchanged (**metric values must not change**), `make api-bench-quick` shows the target route under budget and no other route regressed more than 10 %.
- [ ] **Step 4:** Update the finding's status in `docs/perf/api-findings.md`; commit as `perf(<area>): <what>`.

### Task 7: Regression gate, site benchmark, report

- [ ] **Step 1:** `scripts/api-bench.sh compare` fails if any route's p95 is more than 25 % above `docs/perf/api-baseline-*.md` (tolerance configurable). GitHub Actions stays manual (`workflow_dispatch`) until the user re-enables CI; wire a `workflow_dispatch` job only.
- [ ] **Step 2:** `supermetry-site`: after site job 01 lands, add a small static-serving benchmark (requests/s and p99 for `/`, `/pricing`, a docs page, a 404; with and without `Accept-Encoding: br`) using the same runner via a tiny route list file.
- [ ] **Step 3:** Write `docs/perf/api-report-<date>.md`: what was measured, before/after table, what was fixed, what remains; add dated entries to `progress.md` and durable decisions (budgets, gate tolerance) to `memory.md`.

---

## Running it with Codex and saving tokens

- **Extract a task into a job prompt** (cheap): `awk '/^### Task N:/,/^### Task N\+1:/' docs/superpowers/plans/2026-10-12-api-tests-and-performance.md > /tmp/job-N.md` then prepend the Global Constraints section. Run `SKILLS="golang-testing golang-performance" scripts/codex-job.sh api-t3a /tmp/job-N.md` (add `supabase-postgres-best-practices` for any job touching SQL, `golang-concurrency` for the cache/singleflight work). The job runs in its own worktree on its own branch.
- **Order:** Task 0 → 1 → 2 → (3a, then 3b and 3c in parallel, max 2 at a time) → 4 → 5 → 6 → 7. Tasks 3 and 4 can overlap once Task 1's inventory exists and 3a has landed `internal/testapi`.
- **Claude's token use stays small:** specs are written once here; Claude never reads raw test or benchmark output, only the generated summaries (`inventory.md`, top-20 benchmark rows, `api-findings.md`), the Codex log tail, and the diff stat; verification is running the acceptance commands, not reading code. Wide sweeps (for example "which handlers loop over queries") go to an Explore subagent or to the benchmark's `db_queries` column, not to manual reading.
- **Claude verifies before reporting done:** runs each task's acceptance commands, checks `git log` identity/messages, scans `git diff` for emails, spot-checks the diff of production-code changes. Push only when the user asks.

## Open decisions for the user

1. Approve the proposed latency budgets (Task 5, step 2) or give your own.
2. Go-ahead for the template-database speed-up and Go test cache (Task 0, step 3).
3. OK to add a request-scoped pgx tracer that exists only in test/bench builds.
4. For a test that exposes a bug: leave it failing until fixed (my default) or skip it with a bug id.
5. Dataset sizes beyond tier M wait for the real-VPS run.
