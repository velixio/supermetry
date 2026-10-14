# Pixel analytics project type: design

Status: proposed, 2026-10-14. Scope agreed in chat: developer pixels, aggregate only, no email; plus badge branding, linked snippets and an open counter service (parts 2 and 3).

## Goal

Count views where JavaScript can't run, starting with GitHub profiles, READMEs and docs, without tracking people. A project is either **Web analytics** (today's product) or **Pixel analytics** (new).

## Non-goals

- Email open tracking, per-recipient IDs, or anything that identifies a viewer.
- Unique visitors, sessions, referrers or devices for pixel views. Image proxies hide them, so the UI says "views".
- Converting a project between types after creation.

## Constraints (from CLAUDE.md and goal.md)

- No DB read on the hot path. The badge total comes from an in-memory cache.
- Daily aggregates only. Never store raw IPs or user agents.
- Forward-only migration `0160_pixel_projects.sql`.

## Data model

- `sites.kind text NOT NULL DEFAULT 'web' CHECK (kind IN ('web','pixel'))`. Existing sites stay `web`.
- `pixels`:

  | column | type | notes |
  |---|---|---|
  | `id` | bigint identity | |
  | `site_id` | integer | FK, ON DELETE CASCADE |
  | `public_id` | text | 12 chars, unique, used in URLs |
  | `name` | text | 1–120 chars, e.g. "GitHub profile" |
  | `badge_label` | text | default `'views'`, max 40 chars |
  | `enabled` | bool | default true |
  | `created_at` | timestamptz | |

- `pixel_views_daily(pixel_id, day, country, via, views)`:
  - `via` is `'proxy'` or `'direct'`.
  - Primary key `(pixel_id, day, country, via)`.
  - `country` is `''` for proxied views.
- The existing `pixel_impressions_daily` and `/px/<site public id>.gif?c=` keep working on web projects, unchanged.

## Endpoints

- `GET /px/{pixel public id}.gif`: a 1×1 transparent GIF, always 200 with `no-store`.
- `GET /px/{pixel public id}.svg`: a view-count badge (`label | 1.2k`). It also counts a view.
- Lookup is in memory, the same way `Sites.ByPublicID` works: a pixel registry loaded at start and refreshed when pixels change.
- The URL namespaces can't collide: pixel public IDs get a `p` prefix, while site public IDs are hostnames or 12-character IDs.

## Counting rules

1. Known image proxies are counted as `via=proxy` and skip the bot list. The allowlist matches GitHub `github-camo` and Google `GoogleImageProxy` by user agent.
   - The UA is easy to spoof, so the proxy limiter below caps abuse.
   - IP-range verification is a later improvement.
2. Proxy rate limit: a token bucket keyed by `pixel id + proxy name`, 20 per second with a burst of 200. It is separate from the per-IP limiter, so one busy README doesn't starve others.
3. Direct requests use today's path: per-IP limiter, bot check, DNT/GPC if the site respects them, and a country lookup from the IP. The IP is never stored.
4. A disabled or unknown pixel returns the image and counts nothing.

## Badge total cache

- The recorder already buffers counts in memory and flushes them.
- A `TotalCache` holds `pixel id → all-time views`. It is seeded from Postgres when the registry loads and incremented in memory on every counted view.
- The badge shows the cached value, so it is never more than one process restart out of date and never reads the DB on a request.

## API (private, documented in OpenAPI)

- `POST /api/private/sites` and `/api/public/v1/sites` accept `kind` (default `web`). A pixel project needs no hosts or revenue currency.
- `GET/POST /api/private/sites/{site}/pixels` and `PATCH/DELETE /api/private/sites/{site}/pixels/{id}`.
- `GET /api/private/sites/{site}/pixels/stats?period=…&pixel=…` returns:
  - a daily series split by `via`
  - totals per pixel
  - countries (direct views only)

## UI

- Site creation asks: "What do you want to measure?" with two choices, **Website** (web analytics) and **Views anywhere** (pixel analytics: GitHub, READMEs, docs). The pixel flow asks only name and time zone.
- A pixel project's dashboard (`app.$org.$site.index.tsx` branches on `kind`) shows:
  - a views chart with a pixel filter
  - a pixel list showing total and 30-day views, enabled status, copy snippets and a badge preview
  - countries, with a note that GitHub views have none
- Snippets to copy:
  - Markdown badge `![views](…/px/ID.svg)`
  - invisible Markdown `![](…/px/ID.gif)`
  - HTML `<img>`
- The sidebar hides web-only pages for pixel projects: realtime, sessions, funnels, replay and so on.
- The copy says "views, not unique visitors" next to the chart.

## Errors and edge cases

- Image endpoints never return an error status, so a page never shows a broken image.
- Pixel limit per project: 100. Creating the 101st returns `409 problem+json`.
- Deleting a pixel cascades its daily rows.
- Retention follows the project's retention class, using the same purge job as other daily tables.

## Testing

- **Unit:** proxy detection, the proxy limiter, SVG rendering with number formatting (999, 1.2k, 3.4M) and escaped labels, cache increments, and registry lookup.
- **Handler:** a Camo UA is counted as proxy, while a bot UA, a disabled pixel or an unknown ID are not counted but still return the image. It returns GIF bytes and valid SVG.
- **Store:** an upsert round trip, and stats against a hand-computed fixture.
- **Migration:** existing sites default to `web`.
- **E2E:** create a pixel project, add a pixel, hit its `.svg` with a Camo UA, and see the view on the dashboard.

## Part 2: badge branding and linked snippets

- The badge has a small Supermetry mark on the left of the label. The setting `SUPERMETRY_BADGE_BRANDING` defaults to `true`; self-hosters set `false` to drop the mark.
- Badge style is shields.io-like flat, with 20 px height and colours from the design tokens. `?style=flat|flat-square` and `?color=<hex>` are optional, validated, and fall back to the default.
- The default Markdown snippet is linked: `[![views](BASE/px/ID.svg)](HOMEPAGE/?ref=badge)`, where `HOMEPAGE` is `SUPERMETRY_HOMEPAGE_URL` (D52). The UI also offers an unlinked variant.

## Part 3: open counters (the hosted counter service)

A hits.sh-style counter that needs no account. The engine lives in core, off by default. supermetry.com enables it, and that deployment work goes in `supermetry-cloud`.

- **Switch:** `SUPERMETRY_OPEN_COUNTERS=true` (default `false`). When off, the routes return 404.
- **URLs:** `GET /c/github.com/{owner}.svg` and `GET /c/github.com/{owner}/{repo}.svg`, plus a `.gif` variant.
  - Only GitHub-shaped keys are accepted: an owner of 1–39 chars `[A-Za-z0-9-]`, and a repo of 1–100 chars `[A-Za-z0-9._-]`, lower-cased.
  - Restricting the keys stops free-text spam keys and offensive labels, because the label is fixed to `views`.
  - Other hosts can be added to the allowlist later.
- **Storage:**
  - `open_counters(id, key unique, created_at, last_seen_day)`
  - `open_counter_views_daily(counter_id, day, views)`
  - A counter row is created on its first counted view, through the buffered recorder: an in-memory key → id map, with batch insert on flush.
- **Counting:** only proxied views (GitHub Camo) count. Direct browser hits get the badge but aren't counted, so curl loops can't inflate numbers.
  - There is a per-counter proxy limiter, plus a global cap on new counters: 50 per minute, with a burst of 500.
  - Over the cap, the badge is still served, but no new counter is created.
- **Totals:** the same in-memory total cache as pixels. Cold keys are loaded lazily in batches on the flush tick, so a request never reads the DB. A key not yet loaded shows its last known value, or `0` before the first load.
- **Cleanup:** counters with no view in 365 days are deleted by the daily job. Daily rows older than 400 days are removed with `DELETE` on this small table; it isn't partitioned, so this is an exception to the drop-partition rule.
- **Public stats page:** `/c/github.com/{owner}[/{repo}]` without an extension renders a tiny static HTML page with the total and a 30-day sparkline, using server-rendered SVG with no SPA. It links "Track your own site with Supermetry".
- **Privacy:** no IPs, no user agents and no countries are stored. The supermetry.com privacy policy and terms cover the service; that's supermetry-site work.
- **Cloud (`supermetry-cloud`):** turn on the switch, put `counter.supermetry.com` behind the CDN with no caching for `.svg`/`.gif`, add monitoring and an abuse blocklist file (`SUPERMETRY_OPEN_COUNTER_BLOCKLIST`, one key per line).

## Later

- Pixel email use, which needs a consent story.
- Verifying Camo by IP range.
- More open-counter hosts (GitLab, Codeberg, npm).
