# App Health Phase 4: rollups, crash-free rates, read and status APIs

Brief: `2026-11-15-app-health-phase4-brief.md` (binding). Spec: `../specs/2026-11-15-app-health-design.md`.

## Layers (one commit each, each builds and vets)

1. **Release ordering** `internal/apprelease`: `CompareReleases(a, b, firstSeen)`; semver (leading `v`,
   pre-release sorts lower, build metadata ignored) when both parse, else first-seen, else string order.
   Test first with the brief's four cases plus ties.
2. **Migrations 0260-0263**: `app_issue_daily`, `app_issue_hourly`, `app_release_daily`, and
   `app_issues.resolved_next boolean` (the "resolve in next release" flag). Register the three tables in
   storageusage and in `retention.eraseTables`; retention deletes old rollup rows per site retention.
3. **Rollups** `internal/approllup`: `RecomputeDay(ctx, pool, siteID, day)` deletes and inserts one site-local
   day in one transaction. Day bounds are computed in Go (no SQL tz database needed), so a +05:45 site works.
   Hourly rows are UTC hours computed over whole hours so a day boundary inside an hour stays consistent.
   `Job` (every 10 min, today plus `App.MaxReportAgeDays` days) is added to `scheduledJobs`;
   `recompute-rollups` also rebuilds every app day.
4. **Writer carry-overs**: regression rule moves to Go with `CompareReleases` (upsert no longer touches
   status); one retry after 1 s when the transaction rolled back, then drop and count. Tests first.
5. **Queries** `internal/appquery`: overview, issues, issue detail, compatibility, releases, `SetStatus`.
   Every read bounds `ts >= now - retention`. Period crash-free rates come from raw `app_events`.
6. **HTTP + OpenAPI**: handlers in `internal/platform/http/handlers_app_health.go`, routes under
   `/api/private/sites/{site}/app/...`, audit `app.issue_status_changed`; regenerate Go and web types.

## Definitions (fixture-tested)

- Crash event: `kind='crash' or level='fatal'`, session pings excluded from `events`/`crashes`.
- `events`/`crashes` sum `occurrences` (same unit as `app_issues.event_count`).
- Crash-free users = 1 - crashed installs / installs with any event (non-null hash); zero denominator is null.
- Crash-free sessions = 1 - crashed sessions / sessions with a session ping; none is null.

## Rulings made (also in the report)

- Roles: "member" is the existing `editor` role. Read = owner/admin/editor/viewer (`stats.read`); PATCH =
  owner/admin/editor (`site.edit`).
- `from`/`to` outside retention are clamped, not rejected.
- `environment` is not a rollup dimension, so a series with an environment filter is bucketed from raw events.
- Resolve "next" stores the newest release seen; resolved-next issues regress on events from a release <= it,
  explicit `resolved_in_release` R regresses on events >= R, null regresses on anything.

## Verification

`go build ./... && go vet ./...`, `go test ./internal/... -count=1` with `SUPERMETRY_TEST_DSN`,
`cd web && bun run typecheck && bun run test`.
