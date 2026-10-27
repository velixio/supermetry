# GitHub star tracking: design

Date: 2026-10-22, revised 2026-10-27. Status: accepted. Originally an instance-admin tool for our own repository; the owner then made it an ordinary organization feature (migration `0246_github_stars_per_org.sql`), like the tracking pixel. Where this document says "the organization", it used to say "the instance admin".

## Intent

Record the star history of the GitHub repositories an organization chooses to track (a limited number per organization, 5 by default) so growth, launch spikes and unstars can be charted by that organization's members. History cannot be backfilled later: GitHub never reports unstars, and it keeps traffic data for only 14 days.

## Goals

- Live star count without polling GitHub: a repository `star` webhook pushes every star and unstar with the user and a timestamp.
- Per-user event log (login and numeric id) so each star and unstar is attributable.
- Hourly and daily aggregates for charts (growth, hour-of-day and day-of-week patterns), bucketed in a configured time zone.
- A daily reconcile job that repairs missed webhooks, plus a one-time backfill of existing stargazers.
- An organization-scoped read API and a dashboard page with a live counter, readable by every member of the organization and managed by its owners and admins.

## Non-goals

- No profile enrichment (followers, company, location), fake-star scoring, cohorts or competitor repos in v1.
- No public API in v1. The only public surface is the opt-in star counter described below.
- Not wired to the analytics sites of an organization: star data is its own dataset, not site analytics.
- No instance-wide view: an instance admin sees a repository only as a member of the owning organization.

## Anti-goals

- Never store stargazer emails, or any GitHub data beyond login, numeric id and star timestamps.
- Stargazer logins and ids are visible to members of the owning organization only. Never expose them to other organizations, to instance admins who are not members, or on any public route. (This replaces the earlier "instance admin only" rule.)
- With no tracked repository nothing runs and no outbound request is made.

## Constraints

- Go + PostgreSQL only; no new services or extensions. Forward-only migration.
- Webhook requests are verified with `X-GitHub-Delivery`-keyed idempotency and an HMAC-SHA256 `X-Hub-Signature-256` check using constant-time comparison. Invalid signature gives 401 and nothing is stored. Body size is capped.
- The GitHub token and webhook secret come from config/env, are never logged, and are never committed.
- Respect the repo rules: test-first, hand-computed fixtures for aggregates, PR not direct push, no AI trailers in commits.

## Design

Configuration: `SUPERMETRY_MAX_GITHUB_REPOS_PER_ORG` (default 5) is the instance default for how many repositories one organization may track. An instance admin can override it per organization (`orgs.github_star_repo_limit`: NULL uses the default, a number is the cap, 0 allows none, -1 is unlimited) through `PUT /api/private/admin/organizations/{org}/github-star-repo-limit` with `{"limit": N | null}` (null is unlimited); `DELETE` on the same path restores the default, and `GET` shows the effective limit and how many repositories are tracked. Lowering a limit below the current count deletes nothing: existing repositories keep syncing and only new ones are refused (409). Repositories are added on the GitHub stars page of the organization. With zero repositories nothing runs and the list endpoint returns an empty list. The webhook secret and the optional API token are sealed with the instance secret key (the same facility as the other stored secrets) and are never logged or returned, except the webhook secret once at creation and at rotation.

Package `internal/ghstars`.

Tables (migrations `0240_github_stars.sql`, `0241_github_star_erasures.sql`, `0242_github_star_repos.sql`, `0243_github_star_backfill.sql`, re-keyed by `0246_github_stars_per_org.sql`). Every data table below is keyed by `repo_id`, a foreign key to `github_star_repos(id)` with `ON DELETE CASCADE`, so removing a repository, or its organization, removes its data in one step and organizations never mix. The listings show the shape after `0246`:
- `github_star_events(id bigserial pk, delivery_id text unique null, repo_id bigint, user_id bigint, login text, action text check in ('starred','unstarred'), occurred_at timestamptz, source text check in ('webhook','backfill','reconcile'))`, with `unique (repo_id, delivery_id)`. Index on `(repo_id, occurred_at)` and `(repo_id, user_id, occurred_at)`.
- `github_star_state(repo_id bigint pk, stars int, forks int, watchers int, open_issues int, updated_at timestamptz)`: the live row.
- `github_star_snapshots(repo_id bigint, day date, stars int, forks int, watchers int, open_issues int, primary key(repo_id, day))`: one row per day from the reconcile job, upserted.
- `github_star_annotations(id bigserial pk, repo_id bigint, at timestamptz, label text, created_by bigint)`: manual launch markers.
- `github_star_erasures(repo_id, user_id, erased_at)`: tombstones written by per-user erasure, keyed by repository so an erasure in one organization never affects another; the webhook, reconcile and backfill skip these ids for that repository.
- `github_star_repos(id, org_id, public_id, repo, public_counter, enabled, timezone, webhook_secret_enc, token_enc, created_at, last_webhook_at, last_reconcile_at, last_error)`: the tracked repositories. `org_id` is the owning organization (`ON DELETE CASCADE`); `repo` is stored lowercase `owner/name` and is unique per organization (`unique (org_id, repo)`), so two organizations can track the same GitHub repository independently, each with its own `public_id` and webhook secret; `public_counter` (default false) opts the repository into the public star counter. Existing repositories were assigned to the lowest-id organization by `0246` with `public_counter = true`, so the public counter kept working; `public_id` is a random 16-character id in the webhook URL. Removing a repository with its data deletes the repository row, and the foreign keys delete its rows in every table above in the same transaction.

Webhook `POST /api/github/webhook/{public_id}` (public path, exempt from CSRF and auth): the public id selects the repository (404 when unknown or disabled), then the `X-Hub-Signature-256` HMAC is verified with that repository's own secret before the body is parsed. It handles `star` events (`created`, `deleted`) and `ping`; everything else, and payloads whose `repository.full_name` is not the stored repository, return 202 and are ignored. Insert is idempotent on `delivery_id`; the live row is set from `repository.stargazers_count`. A duplicate delivery returns 200 without writing. Unstar uses delivery time as `occurred_at`, since `starred_at` is null for `deleted`. A verified delivery updates `last_webhook_at`.

Reconcile job (daily and once at startup, via `internal/scheduler`): for each enabled repository, with its own token and time zone, fetch the repo, upsert today's snapshot, compare `stargazers_count` with the live row, and when they differ page through stargazers (`application/vnd.github.star+json`) and insert missing stars (`source=reconcile`) and unstars for users no longer present. Backfill is the same code path when the repository has no events, with `source=backfill`. Repositories of every live organization are handled by the same job. They are isolated: a 401/403/404/429 or any other failure is stored in `last_error` (and `last_reconcile_at` is set) for that repository only and the others continue. A global cap of 2 repositories syncing at once, and a one-second pause between starts, keep many organizations from stampeding GitHub; the ten-minute import job shares both. Repositories of a deleted organization are skipped. The GitHub host is fixed, redirects are off and requests time out. An owner or admin can also start the same run for one repository ("Sync now"): it runs in the background, at most one per repository, manual starts at least a minute apart.

Large repositories: the REST stargazer listing has a depth cap (GitHub answers 422 "pagination is limited" far below 82,000 entries) and a deep page can be slow, so a single run cannot finish. Repositories with more than 30,000 stars, or any repository for which REST answers that 422, use a resumable GraphQL import instead (token required): stargazers are read newest to oldest in chunks of about four minutes, the cursor is saved with every page in the same transaction as its events (`backfill_cursor`), users already on record and erased users are skipped, and `backfill_complete` is set when the history ends. A ten-minute scheduler job continues unfinished imports (a no-op for everything else); the daily job, the job and "Sync now" share one lock per repository. Once complete, each run reads only the newest pages until one is entirely on record and inserts the new stars as `reconcile`. Limit: unstars cannot be detected from a partial walk, so for these repositories the webhook is the only source of unstars. The summary reports `recorded` (current stargazers on record) and the page shows "History import: N of M stars" while it is below the live count. Small repositories keep the full REST diff and mark their history complete.

Organization API, under `/api/private/orgs/{org}/github-stars` (the old instance-admin routes under `/api/private/admin/github-stars` are gone). Every member of the organization (owner, admin, editor, viewer) can read; owners and admins manage (create, patch, delete, rotate, sync, annotations, erasure). Another organization's member gets 404, and a repository id from another organization is 404 on every route. API keys and share links have no access. Added to `openapi.yaml` and the generated client; every change is audited against the organization without secrets (`github_stars.*`):
- `GET /repos`, `POST /repos` (checks the repository on GitHub with one call, tolerating an outage or rate limit; returns the webhook secret and URL once; the URL is built from the instance base URL, or is a path when none is set), `PATCH /repos/{id}` (enabled, time zone, set or clear token), `DELETE /repos/{id}?delete_data=` (`false` disables and keeps history, `true` removes the repository and all its data), `POST /repos/{id}/rotate-secret`, `POST /repos/{id}/sync`.
- `POST /repos` is refused with 409 when the organization already tracks as many repositories as it may. `POST` and `PATCH` also take `public_counter`.
- The remaining endpoints take a required `repo_id` query parameter:
- `GET /summary`: live counts, 24h and 7d net change.
- `GET /series?granularity=hour|day|month&from=&to=`: gained, lost and net per bucket plus cumulative stars, bucketed in the configured time zone. `month` buckets are calendar months in that zone (so Asia/Kolkata shifts a boundary event near midnight UTC into the next month); the default range is the last 12 months, starting no earlier than the first event or snapshot on record; month ranges are limited to 20 years.
- `GET /events?cursor=&limit=`: the user-attributed log, newest first.
- `GET/POST/DELETE /annotations`.
- `DELETE /users/{login}`: deletes that user's events (privacy erasure; aggregates are recomputed from remaining events and snapshots keep the totals) The numeric user id is also written to a tombstone table (`github_star_erasures`, migration `0241`, no login) in the same transaction, and the webhook, reconcile and backfill skip tombstoned ids, so a person who is still starring on GitHub is not re-recorded. The live counters still follow GitHub.

Public count: `GET /api/github/stars/{owner}/{name}` (also `HEAD` and CORS `OPTIONS`, no auth) returns `{"stars":N}` and nothing else, read from the stored live count (never from GitHub), with `Access-Control-Allow-Origin: *` and `Cache-Control: public, max-age=60`. It is opt-in per repository (`public_counter`, off by default, toggled in the repository settings) and GitHub-verified: it serves `verified_stars`, the count the reconcile and import jobs last read from GitHub itself (stored with `verified_at` on `github_star_repos`), never the live count, which webhook payloads update and which the organization that configured the repository signs with its own secret. Without this, any organization could claim someone else's `owner/name` and publish a forged number. A repository that has never been read from GitHub is not served. Only enabled, public repositories of live organizations are candidates, and when several organizations publish the same `owner/name` the one with the most recent successful read from GitHub answers; a claimant whose reconcile keeps failing is never preferred over a newer verified one, and one that was never verified is not served. Migration `0246` seeds `verified_stars` from the stored count for repositories that had already reconciled without error. Unknown, disabled, never-synced and non-public repositories all answer the same 404 `{"error":"not found"}`. It is rate limited per client IP (429 with a JSON error). The repository page shows its URL when the counter is public.

Dashboard: an ordinary organization page at `/app/{org}/github-stars` (and `/app/{org}/github-stars/{owner}/{name}`), with a sidebar item in the Manage group and a command-palette entry for every member. `/app/github-stars` and `/app/admin/github-stars` redirect into the user's default organization page. Owners and admins see: an Add repository dialog (with the public counter option), a per-repository status line (last webhook, last sync, last error, enabled toggle, Sync now, Settings, Rotate secret, Remove with a data choice), the webhook URL, a one-time panel showing the URL, secret and GitHub setup steps after create or rotate, annotation and erase-user controls, and an empty state that invites adding the first repository. Plain members get the same page read-only: those controls are hidden and the empty state asks them to contact an owner or admin. For each repository: a live counter (polls summary every 10 s), a gained/lost/cumulative chart with hour, day and month modes, annotation markers, and an events table.

## Left to the builder

File layout within `internal/ghstars`, exact SQL, pagination details, and chart styling, following the surrounding code. Phase the work as: migration and store, webhook, reconcile and backfill, organization API and OpenAPI, dashboard.

## Calls made without asking

Event table keyed by numeric user id with login stored alongside; daily (not hourly) reconcile; organization-member visibility (owner decision, 2026-10-27, replacing admin-only); delete-by-login for erasure, per repository; repositories configured in the dashboard (not env vars), each with its own time zone, capped per organization (default 5); time-zone bucketing per repository.

## Privacy note

A stargazer's GitHub login and numeric id are public GitHub data, but the event log is still personal data we store. It is visible only to members of the organization that tracks the repository: no other organization, no public route, and no instance admin who is not also a member. Erasure is per repository: deleting a login's events writes a tombstone keyed by the repository id (numeric id only, no login), so the same user in another organization's copy of the same GitHub repository is untouched, and removing a repository or an organization deletes its events, state, snapshots, annotations and tombstones. Tokens and webhook secrets are sealed with the instance secret key and never logged or returned.
