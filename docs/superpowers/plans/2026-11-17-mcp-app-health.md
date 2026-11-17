# MCP App Health Tools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** AI assistants connected to Supermetry's existing MCP server (`POST /api/public/v1/mcp`) can read App Health: overview, issues, one issue, compatibility and releases.

**Architecture:** Add five read-only tools to `internal/api/mcp.go`, backed by the existing `internal/appquery.Querier` through a small interface in `MCPDeps`. Site kind comes from the in-memory `sites.Registry`. The same API key, `stats:read` scope, site restriction and rate limit apply. No new endpoint, migration or SQL.

**Tech Stack:** Go, JSON-RPC 2.0 / MCP 2025-06-18 (already implemented), pgx via `appquery`.

**Program:** this plan is Phase 1a's first PR in `docs/superpowers/specs/2026-11-17-app-monitoring-platform-design.md` (§8).

**Spec:** this file (small extension; App Health semantics are in `docs/superpowers/specs/2026-11-15-app-health-design.md` §5–7).

## Current state (2026-11-17)

- MCP exists: `internal/api/mcp.go` (protocol 2025-06-18, legacy 2025-03-26), routed in `internal/api/router.go:141-145`, documented in `docs/api.md` "MCP for AI assistants".
- Tools: `list_sites`, `get_summary`, `get_timeseries`, `get_breakdown`, `get_realtime`, `list_goals`, `run_funnel`. All web analytics.
- App Health reads exist only on the private API (`internal/platform/http/handlers_app_health.go`, `appquery.Querier`). Nothing exposes them to API keys or MCP.

## Global Constraints

- Read-only. No status changes (resolve/ignore) over MCP in this plan; that would need a write scope (deferred).
- Every app tool: `requireSite(r, site)` first (key org + site restriction, then `stats:read`), and an unknown/foreign site gives the same `errMCPSiteNotFound` as today.
- App tools work in every tracking mode (they hold no visitor history): exempt them from the `ErrAnonymousHistory` gate in `runTool`.
- Output never contains install/session hashes, keys, or exception messages (`appquery` types already omit them; keep it that way).
- `get_app_issue` returns at most 5 latest events; frames are the server-scrubbed string as stored.
- Period input reuses `mcpPeriodRange` and the existing `period` enum, or `date_from`/`date_to` (inclusive, ≤ 366 days apart), in the site's time zone.
- Filters: `release` ≤ 64 chars, `environment` ≤ 32, `platform` ≤ 32 (same limits as `handlers_app_health.go:17-21`).
- Commits: identity velixio, no trailers, no AI mentions; scan staged diff for emails. Branch `feat/mcp-app-health`, PR to main.

## Tools

| Tool | Required | Optional | Returns |
|---|---|---|---|
| `get_app_overview` | `site`, period or dates | `release`, `environment`, `platform` | `appquery.Overview` |
| `list_app_issues` | `site`, period or dates | filters, `status` (`open`·`regressed`·`resolved`·`ignored`·`all`, default `open`), `kind`, `q` (≤100), `sort` (`last_seen`·`events`·`users`), `limit` 1–100 (default 25), `offset` ≥0 | `appquery.IssueList` |
| `get_app_issue` | `site`, `issue_id` (int ≥1), period or dates | filters | `appquery.IssueDetail`, `latest_events` truncated to 5 |
| `get_app_compatibility` | `site`, period or dates | `platform` | `{"rows": []appquery.CompatRow}` |
| `list_app_releases` | `site`, period or dates | `environment`, `platform` | `{"releases": []appquery.ReleaseRow}` |

Tool descriptions say "App projects only." so the assistant picks the right family.

## Review Focus

1. App tool on a **web or pixel** site → JSON-RPC invalid params `"site is not an app; use get_summary and the other analytics tools"`, not empty data. Pin in Task 2.
2. Web tool (`get_summary`, `get_timeseries`, `get_breakdown`, `get_realtime`, `run_funnel`) on an **app** site → invalid params `"site is an app; use the get_app_* tools"`. `list_sites` and `list_goals` unchanged. Pin in Task 2.
3. `get_app_issue` with an issue id from **another site** of the same org → not found (`appquery.Issue` scopes by site; assert it). Pin in Task 3.
4. Key restricted to site A calling an app tool for site B → same not-found as today; key without `stats:read` → -32003 with `WWW-Authenticate`. Pin in Task 3 (extend `TestMCPDirectQueriesKeepTenantAndScopeChecks`).
5. Anonymous tracking mode: app tools still work; `get_realtime` still refused. Pin in Task 2.

---

### Task 1: Wiring and the reader interface

**Files:**
- Modify: `internal/api/mcp.go` (MCPDeps), `internal/api/router.go:141-144` and `api.Deps`
- Modify: `cmd/supermetry/serve.go:504`, `internal/testenv/testenv.go:141`, `internal/testapi/testapi.go:141`
- Create: `internal/api/mcp_app.go`

**Interfaces:**
- Produces, in `mcp_app.go`:
  ```go
  type appHealthReader interface {
      Overview(ctx context.Context, scope appquery.Scope, f appquery.Filter) (appquery.Overview, error)
      Issues(ctx context.Context, scope appquery.Scope, in appquery.IssueQuery) (appquery.IssueList, error)
      Issue(ctx context.Context, scope appquery.Scope, id int64, f appquery.Filter) (appquery.IssueDetail, error)
      Compatibility(ctx context.Context, scope appquery.Scope, f appquery.Filter) ([]appquery.CompatRow, error)
      Releases(ctx context.Context, scope appquery.Scope, f appquery.Filter) ([]appquery.ReleaseRow, error)
  }
  ```
  `*appquery.Querier` satisfies it (compile-time assert `var _ appHealthReader = (*appquery.Querier)(nil)`).
- `MCPDeps` gains `AppHealth appHealthReader` and `Sites sites.Registry`; `api.Deps` gains `AppQuery *appquery.Querier`, set in serve.go as `appquery.New(pool, now)` and in testenv/testapi likewise. Router passes `deps.AppQuery` and `deps.Sites`.
- Nil `AppHealth` → app tools are omitted from `tools/list` (keeps existing tests that build `MCPDeps` by hand green).

- [ ] Step 1: `go build ./... && go vet ./internal/api ./cmd/...` passes; existing `go test ./internal/api -run MCP -count=1` (with `SUPERMETRY_TEST_DSN`) still passes.
- [ ] Step 2: Commit `api: wire app health reads into MCP deps`.

### Task 2: Tool schemas, dispatch and kind checks

**Files:**
- Modify: `internal/api/mcp.go` (`mcpTools`, `runTool` gate and switch)
- Modify: `internal/api/mcp_app.go` (inputs, `appScope`, `appFilter`, handlers)
- Test: `internal/api/mcp_app_test.go` (stub `appHealthReader`, stub registry)

**Interfaces:**
- Consumes: Task 1.
- Produces: `func (h *mcpHandler) runAppTool(ctx context.Context, r *http.Request, name string, args json.RawMessage) (any, error)`; `func mcpAppScope(site sites.Site) appquery.Scope` (same as `handlers_app_health.go:49`: TZ, retention); `func (in mcpAppRange) filter(loc *time.Location, now time.Time) (appquery.Filter, error)`.

- [ ] Step 1: Write failing tests in `mcp_app_test.go`:
  - `TestMCPAppToolsListed`: `tools/list` contains the five names, each schema has `additionalProperties:false` and requires `site`.
  - `TestMCPAppToolsCall`: table over the five tools with a stub reader; each returns `"type":"text"` and a field from the stub (`crash_free_users`, an issue title, `os_version`, a release `version`). Assert the stub received `Filter{From: 2026-10-02, To: 2026-10-08}` for `period:"7d"` with `Now` = 2026-10-08 12:00 UTC and site TZ UTC.
  - `TestMCPAppIssueLatestEventsCapped`: stub returns 12 latest events → response has 5.
  - `TestMCPAppToolsRejectWrongKind` (Review Focus 1, 2): app tool on web site and `get_summary` on app site → error code -32602 with the exact messages above.
  - `TestMCPAppToolsAnonymousMode` (Review Focus 5): `TrackingMode: privacy.Anonymous`, `get_app_overview` succeeds, `get_realtime` still returns the anonymous-history error.
  - Input validation: `limit:101`, `sort:"bogus"`, `status:"bogus"`, `release` of 65 chars, `date_from` after `date_to`, range > 366 days, and both `period` and dates → -32602.
- [ ] Step 2: Run `go test ./internal/api -run MCPApp -count=1`; expect FAIL (unknown tool).
- [ ] Step 3: Implement. Look up kind with `h.deps.Sites.Site(int32(site))` after `requireSite` succeeds; a missing registry entry → `errMCPSiteNotFound`. Map `ErrNotFound`-style results from `appquery.Issue` (check the sentinel it returns) to `errMCPSiteNotFound`-like "issue not found" invalid params.
- [ ] Step 4: Run the tests again plus `go test ./internal/api -run MCP -count=1`; expect PASS.
- [ ] Step 5: Commit `api: app health tools for MCP`.

### Task 3: Tenant and scope boundaries against a real DB

**Files:**
- Modify: `internal/api/sensitive_mcp_test.go` (or a new `mcp_app_tenant_test.go` using `testenv`)

- [ ] Step 1: Tests (real Postgres via `testenv`, seeded with two app sites in org 1 and one in org 2, a few `app_events` + `app_issues`):
  - `TestMCPAppIssueOtherSiteNotFound` (Review Focus 3): issue id from site B requested on site A → not found, no data from B in the body.
  - Extend tenant test (Review Focus 4): key restricted to site A → app tools on site B are not found; key without `stats:read` → -32003 and `WWW-Authenticate: Bearer error="insufficient_scope", scope="stats:read"`; org-2 site → not found.
  - `TestMCPAppOutputHasNoIdentifiers`: the JSON of every tool contains none of the seeded raw install/session ids, their hashes, or the key prefix `smk_`.
- [ ] Step 2: Run, fix anything that fails, then `go test ./internal/api -count=1`.
- [ ] Step 3: Commit `api: pin MCP app health tenant and scope boundaries`.

### Task 4: Docs, changelog, features

**Files:**
- Modify: `docs/api.md` (MCP section: list the five tools, one line each, plus "App tools work in every tracking mode; web tools on an app return an error")
- Modify: `docs/guide/apps.md` (short "Ask your AI assistant" paragraph linking the MCP section)
- Modify: `docs/api-auth.md:33` (`stats:read` row: "…the MCP server, including App Health tools…")
- Modify: `CHANGELOG.md` (new top section, next patch version), `docs/features.md` row 96 note or the App health row ("MCP tools").

- [ ] Step 1: Write the docs; scan for emails; `go test ./internal/platform/http -run OpenAPIDrift -count=1` still passes (no OpenAPI change expected; MCP is JSON-RPC).
- [ ] Step 2: Commit `docs: MCP app health tools`.

### Finish

- [ ] `go build ./... && go vet ./...`; `go test ./internal/... -count=1` with the DSN (known date-dependent failures on main: `TestPrivateAnalyticsMatrixValidMalformedAndTenantBoundaries`, `TestRetentionReportMatchesDailyIdentityFixture`, `TestRetentionReportMatchesProfileFixtureAcrossDays`).
- [ ] Manual check against a throwaway server: `curl` `tools/list` and `tools/call get_app_overview` with a `stats:read` key on a seeded app.
- [ ] Push `feat/mcp-app-health`, open PR. After merge: rebuild `bin/supermetry`, restart the instance, run `bun run sync` in `supermetry-site` (docs page lists the tools) and bump Cloud `CORE_VERSION` in a small PR (the gateway already proxies `/api/public/v1/mcp`; confirm in `internal/gateway`).

## Deferred

- Write tools (`set_app_issue_status`) behind a new `issues:write` scope.
- MCP resources/prompts (e.g. "triage the newest regressions"), streamable-HTTP sessions, OAuth for MCP clients.
