# GitHub star tracking: design

Date: 2026-10-22. Status: proposed (user asked to build it in core; questions skipped, so every choice below is the agent's call).

## Intent

Record the star history of our own GitHub repository so growth, launch spikes and unstars can be charted in the instance admin. History cannot be backfilled later: GitHub never reports unstars, and it keeps traffic data for only 14 days.

## Goals

- Live star count without polling GitHub: a repository `star` webhook pushes every star and unstar with the user and a timestamp.
- Per-user event log (login and numeric id) so each star and unstar is attributable.
- Hourly and daily aggregates for charts (growth, hour-of-day and day-of-week patterns), bucketed in a configured time zone.
- A daily reconcile job that repairs missed webhooks, plus a one-time backfill of existing stargazers.
- Admin-only read API and an admin dashboard chart with a live counter.

## Non-goals

- No profile enrichment (followers, company, location), fake-star scoring, cohorts or competitor repos in v1.
- No public API or public counter in v1.
- No tracking of repositories other than the single configured one.
- Not wired for customer sites: this is instance-admin data about our own repo, not customer analytics.

## Anti-goals

- Never store stargazer emails, or any GitHub data beyond login, numeric id and star timestamps.
- Never expose usernames outside instance-admin routes.
- The feature must be fully off, with no routes active and no jobs running, unless configured.

## Constraints

- Go + PostgreSQL only; no new services or extensions. Forward-only migration.
- Webhook requests are verified with `X-GitHub-Delivery`-keyed idempotency and an HMAC-SHA256 `X-Hub-Signature-256` check using constant-time comparison. Invalid signature gives 401 and nothing is stored. Body size is capped.
- The GitHub token and webhook secret come from config/env, are never logged, and are never committed.
- Respect the repo rules: test-first, hand-computed fixtures for aggregates, PR not direct push, no AI trailers in commits.

## Design

Configuration (all optional; feature off when `SUPERMETRY_GITHUB_STARS_REPO` is empty):
`SUPERMETRY_GITHUB_STARS_REPO` (`owner/name`), `SUPERMETRY_GITHUB_WEBHOOK_SECRET`, `SUPERMETRY_GITHUB_TOKEN` (reconcile and backfill; optional, unauthenticated works at lower limits), `SUPERMETRY_GITHUB_STARS_TZ` (default `UTC`).

Package `internal/ghstars`.

Tables (migration `0240_github_stars.sql`):
- `github_star_events(id bigserial pk, delivery_id text unique null, repo text, user_id bigint, login text, action text check in ('starred','unstarred'), occurred_at timestamptz, source text check in ('webhook','backfill','reconcile'))`. Index on `(repo, occurred_at)` and `(repo, user_id, occurred_at)`.
- `github_star_state(repo text pk, stars int, forks int, watchers int, open_issues int, updated_at timestamptz)`: the live row.
- `github_star_snapshots(repo text, day date, stars int, forks int, watchers int, open_issues int, primary key(repo, day))`: one row per day from the reconcile job, upserted.
- `github_star_annotations(id bigserial pk, repo text, at timestamptz, label text, created_by bigint)`: manual launch markers.

Webhook `POST /api/github/webhook` (public path, exempt from CSRF and auth, signature required): handles `star` events (`created`, `deleted`) and `ping`; everything else returns 202 and is ignored. Insert is idempotent on `delivery_id`; the live row is set from `repository.stargazers_count`. A duplicate delivery returns 200 without writing. Unstar uses delivery time as `occurred_at`, since `starred_at` is null for `deleted`.

Reconcile job (daily, via `internal/scheduler`): fetch the repo, upsert today's snapshot, compare `stargazers_count` with the live row, and when they differ page through stargazers (`application/vnd.github.star+json`) and insert missing stars (`source=reconcile`) and unstars for logins no longer present. Backfill is the same code path run on demand when the events table is empty, with `source=backfill`. GitHub calls go through `internal/safehttp` if it fits, with a bounded timeout and rate-limit awareness (stop on 403/429 and retry next run).

Admin API (instance admin only, under `/api/private/admin/github-stars`, added to `openapi.yaml` and the generated client):
- `GET /summary`: live counts, 24h and 7d net change.
- `GET /series?granularity=hour|day&from=&to=`: gained, lost and net per bucket plus cumulative stars, bucketed in the configured time zone.
- `GET /events?cursor=&limit=`: the user-attributed log, newest first.
- `GET/POST/DELETE /annotations`.
- `DELETE /users/{login}`: deletes that user's events (privacy erasure; aggregates are recomputed from remaining events and snapshots keep the totals).

Dashboard: an Admin > GitHub stars page with a live counter (polls summary every 10 s), a gained/lost/cumulative chart with hour and day modes, annotation markers, and an events table. Follows existing admin page and shadcn chart conventions.

## Left to the builder

File layout within `internal/ghstars`, exact SQL, pagination details, and chart styling, following the surrounding code. Phase the work as: migration and store, webhook, reconcile and backfill, admin API and OpenAPI, dashboard.

## Calls made without asking

Event table keyed by numeric user id with login stored alongside; daily (not hourly) reconcile; admin-only visibility; delete-by-login for erasure; single-repo scope; time-zone bucketing via config.
