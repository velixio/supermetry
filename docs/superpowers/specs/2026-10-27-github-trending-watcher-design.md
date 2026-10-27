# GitHub Trending watcher

Status: implemented (2026-10-27). Package `internal/ghtrending`, migration `0248`.

## Goal

Tell the team when a tracked GitHub repository appears on github.com/trending, keep a history of every appearance, mark it on the star chart, and expose a hook other features (notifications) can use.

## Design

- **Opt-in per repository.** `github_star_repos.trending_enabled` (default false) is set through the existing repository PATCH (`trending_enabled`). `github_star_repos.language` holds the repository's primary language; the star reconcile writes it from the `/repos/{repo}` call it already makes (empty when GitHub reports none).
- **Lists.** For the enabled repositories the job needs, per window (`daily`, `weekly`), the all-languages list (`/trending`) and one list per distinct language (`/trending/{slug}`). A language slug is lowercase, spaces become hyphens, everything else outside `[a-z0-9._-]` is percent-encoded (`c++` -> `c%2B%2B`, `c#` -> `c%23`). A 404 means GitHub has no list for that language; it is paused for 24 hours and does not make the repository "unreadable". Each fetched list serves every repository that needs it, whoever owns it.
- **Fetcher.** Fixed host `https://github.com`, HTTPS only, redirects refused, no cookies, 10 s timeout, 2 MiB cap, a User-Agent that names Supermetry, at most one request per second, at most 12 lists per run. If more are needed, lists are fetched oldest-attempt-first so every list gets its turn over following runs; the last run's postponed count is stored (`github_trending_run`) and shown in the status.
- **Parser.** Tolerant `golang.org/x/net/html` parse. A repository row is an `article.Box-row`; the repository is the link inside its `h2` (other links such as sponsor buttons are ignored). Rank is the row position. A page with no rows, or fewer than 5 readable repositories, is *unreadable*.
- **Episodes** (`github_trending_sightings`, one row per episode). An episode opens when the repository is first seen in a list and stays open while it is present (best and last rank updated). It closes after it is absent from two consecutive readable fetches of that list (`misses`). Re-entry opens a new episode. An unreadable or failed fetch changes no episode: it only sets `github_trending_list_state` (`parse_ok=false` and `last_error` for unreadable pages) and shows as status `unreadable`; the next good fetch clears it. Turning the watcher off closes open episodes without notifying.
- **Job.** `github-trending`, every 10 minutes through `internal/scheduler`, silent when no repository is enabled.
- **Events.** `TrendingNotifier{Entered, Left}` is injected into the service (default no-op). Calls follow the stored transition, at most once, with `Sighting.ID` as the episode key. Wiring to the per-org notifications package is a separate small adapter (not part of this change).
- **Chart marker.** On entry an annotation "On trending: Go, daily, #7" is written once per episode (`annotation_id` on the sighting; a failed write is retried on the next run). `annotate.go` is the only code that knows how the annotation table is keyed.
- **API.** `GET .../github-stars/repos/{id}/trending` returns enabled, language, status (`off`, `waiting`, `watching`, `unreadable`), last check, per-list health, the postponed-list count, current episodes and the latest 50 episodes.
- **UI.** A "GitHub Trending" card on the repository page: switch, status line, current episodes with rank badges, history.

## Caveats

- **No stable contract.** GitHub offers no official API for trending. The page is scraped best-effort and can change or block at any time; the watcher then shows "can't read trending right now" and concludes nothing. Check GitHub's terms of service before relying on it. The polite rate (one request per second, at most 12 per ten minutes, identifying User-Agent, no redirects or cookies) is deliberate and must not be raised casually.
- A malformed row that hides a repository counts as an absence; the two-miss rule absorbs a single bad fetch.
- If a repository's language changes while an episode is open on its old language list, that episode is no longer updated and stays open until the repository is switched off. Rare; accepted.
- Notifications are at-most-once: a crash between the stored transition and the call loses it.

## Privacy

Only public repository names and ranks are read and stored. No user data, IP addresses or cookies are involved; the requests carry no credentials.
