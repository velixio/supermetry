# GitHub star tracking: design

Date: 2026-10-22. Status: proposed (user asked to build it in core; questions skipped, so every choice below is the agent's call).

## Intent

Record the star history of GitHub repositories (any number, chosen by the instance admin) so growth, launch spikes and unstars can be charted in the instance admin. History cannot be backfilled later: GitHub never reports unstars, and it keeps traffic data for only 14 days.

## Goals

- Live star count without polling GitHub: a repository `star` webhook pushes every star and unstar with the user and a timestamp.
- Per-user event log (login and numeric id) so each star and unstar is attributable.
- Hourly and daily aggregates for charts (growth, hour-of-day and day-of-week patterns), bucketed in a configured time zone.
- A daily reconcile job that repairs missed webhooks, plus a one-time backfill of existing stargazers.
- Admin-only read API and an admin dashboard chart with a live counter.

## Non-goals

- No profile enrichment (followers, company, location), fake-star scoring, cohorts or competitor repos in v1.
- No public API or public counter in v1.
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

Configuration: none in the environment. Repositories are added in Admin > GitHub stars (up to 50). With zero repositories nothing runs and the list endpoint returns an empty list. The webhook secret and the optional API token are sealed with the instance secret key (the same facility as the other stored secrets) and are never logged or returned, except the webhook secret once at creation and at rotation.

Package `internal/ghstars`.

Tables (migrations `0240_github_stars.sql`, `0241_github_star_erasures.sql`, `0242_github_star_repos.sql`):
- `github_star_events(id bigserial pk, delivery_id text unique null, repo text, user_id bigint, login text, action text check in ('starred','unstarred'), occurred_at timestamptz, source text check in ('webhook','backfill','reconcile'))`. Index on `(repo, occurred_at)` and `(repo, user_id, occurred_at)`.
- `github_star_state(repo text pk, stars int, forks int, watchers int, open_issues int, updated_at timestamptz)`: the live row.
- `github_star_snapshots(repo text, day date, stars int, forks int, watchers int, open_issues int, primary key(repo, day))`: one row per day from the reconcile job, upserted.
- `github_star_annotations(id bigserial pk, repo text, at timestamptz, label text, created_by bigint)`: manual launch markers.
- `github_star_erasures(repo, user_id, erased_at)`: tombstones written by per-user erasure; the webhook, reconcile and backfill skip these ids.
- `github_star_repos(id, public_id, repo, enabled, timezone, webhook_secret_enc, token_enc, created_at, last_webhook_at, last_reconcile_at, last_error)`: the tracked repositories. `repo` is stored lowercase `owner/name` and is the key the other tables use; `public_id` is a random 16-character id in the webhook URL. Removing a repository with its data deletes its rows from every table above in one transaction.

Webhook `POST /api/github/webhook/{public_id}` (public path, exempt from CSRF and auth): the public id selects the repository (404 when unknown or disabled), then the `X-Hub-Signature-256` HMAC is verified with that repository's own secret before the body is parsed. It handles `star` events (`created`, `deleted`) and `ping`; everything else, and payloads whose `repository.full_name` is not the stored repository, return 202 and are ignored. Insert is idempotent on `delivery_id`; the live row is set from `repository.stargazers_count`. A duplicate delivery returns 200 without writing. Unstar uses delivery time as `occurred_at`, since `starred_at` is null for `deleted`. A verified delivery updates `last_webhook_at`.

Reconcile job (daily and once at startup, via `internal/scheduler`): for each enabled repository, with its own token and time zone, fetch the repo, upsert today's snapshot, compare `stargazers_count` with the live row, and when they differ page through stargazers (`application/vnd.github.star+json`) and insert missing stars (`source=reconcile`) and unstars for users no longer present. Backfill is the same code path when the repository has no events, with `source=backfill`. Repositories are isolated: a 401/403/404/429 or any other failure is stored in `last_error` (and `last_reconcile_at` is set) for that repository and the others continue. The GitHub host is fixed, redirects are off and requests time out. An admin can also start the same run for one repository ("Sync now"): it runs in the background, at most one per repository, manual starts at least a minute apart.

Admin API (instance admin only, under `/api/private/admin/github-stars`, added to `openapi.yaml` and the generated client; every change is audited without secrets):
- `GET /repos`, `POST /repos` (checks the repository on GitHub with one call, tolerating an outage or rate limit; returns the webhook secret and URL once; the URL is built from the instance base URL, or is a path when none is set), `PATCH /repos/{id}` (enabled, time zone, set or clear token), `DELETE /repos/{id}?delete_data=` (`false` disables and keeps history, `true` removes the repository and all its data), `POST /repos/{id}/rotate-secret`, `POST /repos/{id}/sync`.
- The remaining endpoints take a required `repo_id` query parameter:
- `GET /summary`: live counts, 24h and 7d net change.
- `GET /series?granularity=hour|day&from=&to=`: gained, lost and net per bucket plus cumulative stars, bucketed in the configured time zone.
- `GET /events?cursor=&limit=`: the user-attributed log, newest first.
- `GET/POST/DELETE /annotations`.
- `DELETE /users/{login}`: deletes that user's events (privacy erasure; aggregates are recomputed from remaining events and snapshots keep the totals) The numeric user id is also written to a tombstone table (`github_star_erasures`, migration `0241`, no login) in the same transaction, and the webhook, reconcile and backfill skip tombstoned ids, so a person who is still starring on GitHub is not re-recorded. The live counters still follow GitHub.

Dashboard: an Admin > GitHub stars page with a repository selector, an Add repository dialog, a per-repository status line (last webhook, last sync, last error, enabled toggle, Sync now, Disable, Remove with a data choice), a one-time panel showing the webhook URL, secret and GitHub setup steps after create or rotate, an empty state that invites adding the first repository, and, for the selected repository, a live counter (polls summary every 10 s), a gained/lost/cumulative chart with hour and day modes, annotation markers, and an events table. Follows existing admin page and shadcn chart conventions.

## Left to the builder

File layout within `internal/ghstars`, exact SQL, pagination details, and chart styling, following the surrounding code. Phase the work as: migration and store, webhook, reconcile and backfill, admin API and OpenAPI, dashboard.

## Calls made without asking

Event table keyed by numeric user id with login stored alongside; daily (not hourly) reconcile; admin-only visibility; delete-by-login for erasure; any number of repositories configured in the admin UI (not env vars), each with its own time zone; time-zone bucketing per repository.
