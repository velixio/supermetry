# App Health Phase 7 brief: app alerts

Spec: `docs/superpowers/specs/2026-11-15-app-health-design.md` §8 and the "Alerts" call in "Calls made by Claude". Existing system: `internal/alerts` (`AlertKind` in `types.go:43-48`: `traffic_spike`, `traffic_drop`, `goal_reached`; the evaluator, engine, delivery to email/webhook/Slack/Discord/Telegram, templates, history). Phases 3–4 (on this branch) give you `app_issues` (`status`, `first_seen`, `resolved_in_release`), `app_issue_hourly`, `app_release_daily`, `appquery.CompareReleases`, and the crash-free definitions in `internal/appquery`.

## New alert kinds (app sites only; creating one on a web or pixel site → 400)
| Kind | Config | Fires when | Dedup |
|---|---|---|---|
| `app_new_issue` | optional `kinds` filter (`crash`, `error`, …) | an issue's `first_seen` falls inside the last evaluation window | once per issue |
| `app_issue_regressed` | none | an issue's status changed to `regressed` since the last evaluation | once per regression (key: issue id + time of regression) |
| `app_crash_free_below` | `threshold` (percent, 50–100, default 99), `metric` (`users` or `sessions`) | the newest release (per `CompareReleases`) with ≥100 installs in the last 24 h has crash-free below the threshold | at most once per 24 h per alert and release; re-arms after it recovers |
| `app_issue_spike` | none | an issue's events in the last full UTC hour are ≥ 10 and ≥ 3× its average for the same hour of day over the previous 7 days (missing hours count as 0) | once per issue per hour |

- **Evaluation**: polled by the existing alerts engine on its schedule, using the existing interfaces. The engine is the single scheduler; don't add a worker.
- **How regressions are detected**: add `regressed_at timestamptz null` to `app_issues`, migration in 0270–0274. The writer sets it when it marks an issue regressed. Update the Phase 3/4 writer code there.
- **Message templates**: each kind gets a template in the existing template system. Variables: app name, issue title, kind, release, counts, crash-free value and threshold, and the issue URL (`/app/{org}/{site}/issues/{id}`). Never include frames or tags in messages.
- **API and UI**: extend the existing alert CRUD (OpenAPI enum and config schema), then regenerate. In the alert settings UI, an app site shows only the four app kinds, with their config fields. A web site shows only the existing kinds.
- **Tests**:
  - Evaluator unit tests, hand-built:
    - **New issue**: fires on a new issue, doesn't fire twice.
    - **Regression**: fires on a regression.
    - **Crash-free**: fires below the threshold; doesn't fire under 100 installs; respects the 24-hour dedup; re-arms after recovery.
    - **Spike**: fires at 30 events vs a 7-day average of 5; doesn't fire at 9 vs 0 (under the floor); doesn't fire at 20 vs an average of 8 (under 3×).
  - Template rendering tests.
  - An API test: creating an app kind on a web site → 400.
  - A UI test: the alert form for an app shows the four kinds.

## Process
- Write a short plan at `docs/superpowers/plans/2026-11-15-app-health-phase7-alerts.md` (≤100 lines), commit it, then execute it test-first.
- Make 4–6 commits.
