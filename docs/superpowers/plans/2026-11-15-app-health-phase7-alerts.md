# App Health Phase 7: app alerts (plan)

Brief: `2026-11-15-app-health-phase7-brief.md`. Spec section 8.

## Design
- **Kinds**: `app_new_issue`, `app_issue_regressed`, `app_crash_free_below`, `app_issue_spike` added to `AlertKind`. App kinds only on app sites, web/pixel kinds only on non-app sites; the HTTP handler and the service both reject the wrong combination (400).
- **Config**: new `alerts.config jsonb` (migration 0270) holding `{kinds: [...]}` for new issue and `{metric: "users"|"sessions"}` for crash-free; `threshold` holds the crash-free percent (50-100, default 99). Kinds without a threshold store 1 (the column needs > 0).
- **Regression time**: `app_issues.regressed_at` (0271), set by `internal/appingest` where it flips status to `regressed`.
- **Dedup**: `app_alert_fired(alert_id, dedup_key, fired_at)` (0272). Keys: `issue:<id>`, `regress:<id>:<unix>`, `spike:<id>:<hour unix>`, `crashfree:<release>`.
- **Evaluation**: the existing evaluator (one scheduler, 5-minute poll). A new `AppSource` interface (Postgres impl in `internal/alerts/appsource.go`) gives candidate rows; an `AppDedup` interface stores keys. The evaluator turns candidates into notifications (max 20 per alert per run) and reuses the existing channel, delivery-state, history path. The dedup key is written when the delivery state is persisted.
  - new issue: `first_seen` within the last 24 h (look-back, dedup makes it once), optional kind filter.
  - regressed: `regressed_at` within the last 24 h.
  - crash-free: newest release (apprelease order) with >= 100 installs in the last 24 h; value below threshold; dedup key per release re-armed after 24 h (cooldown) and after recovery (a run seeing it at/above the threshold clears the key).
  - spike: last full UTC hour events >= 10 and >= 3x the mean of the same hour on the previous 7 days (missing = 0).
- **Messages**: per-alert-kind default template, new variables (`app_name`, `issue_title`, `issue_kind`, `release`, `events`, `installs`, `crash_free`, `threshold`, `issue_url`, `metric`). No frames or tags. Issue URL `/app/{org slug}/{site slug}/issues/{id}`.
- **UI**: alert form on an app site offers only the four app kinds with config fields; web sites keep the old three.

## Steps (test first)
1. Plan commit.
2. Migrations + writer sets `regressed_at` (writer test first).
3. Kinds, config, validation in service and handler (handler and service tests first).
4. Evaluator, source, dedup (evaluator tests first).
5. Templates and variables (template tests first).
6. Alert settings UI (component test first).

## Gates
`go build ./... && go vet ./...`, `go test ./internal/... -count=1`, `cd web && bun run typecheck && bun run test`.
