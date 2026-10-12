# Supermetry API

Read your analytics, manage sites and send server-side events over HTTP.

- **Base URL:** `https://<your-supermetry-host>/api/public/v1`
- **Auth:** `Authorization: Bearer <api-key>`. See [api-auth.md](api-auth.md) for keys, scopes, errors and rate limits.
- **Spec:** the full OpenAPI 3.1 document is served at `/api/public/openapi.json`. In the dashboard, **Organization settings → API** shows a readable reference with examples and a "Try it" form.
- **Client:** a typed TypeScript client lives in `web/packages/api-client`.
- **AI assistants:** the same data is available to MCP clients at `POST /api/public/v1/mcp` (see the bottom of this page).

## Conventions

- **Sites** are addressed by numeric `site_id`. List them with `GET /sites`.
- **Dates** are `YYYY-MM-DD` in the site's time zone. Ranges are inclusive and can span up to 366 days (except `period=all`).
- **Periods:** `today`, `yesterday`, `7d`, `30d`, `month`, `last_month`, `6mo`, `12mo`, `year`, `all`, or `custom` with `date_from` and `date_to`.
- **Filters:** `filter=dimension:operator:value1,value2`. Repeat the parameter for several filters (up to 10). A filter takes at most 50 values, and all `contains` and `starts_with` values in one request together are capped at 100. Requests over these limits get a 400 problem response. Operators are `is`, `is_not`, `contains` and `starts_with`. Use `GET /sites/{site_id}/suggestions` to find valid values.
- **Dimensions:** `page`, `entry`, `exit`, `source`, `utm_source`, `utm_medium`, `utm_campaign`, `country`, `region`, `city`, `browser`, `os`, `device`, `event`, `goal`, `prop:<key>`, `weekday`, `hour_of_day`, plus `time` and `hour` for time series.
- **Metrics:** `visitors`, `visits`, `pageviews`, `views_per_visit`, `bounce_rate`, `visit_duration`, `engagement_time`, `scroll_depth`, `events`, `conversions`, `conversion_rate`, `total_revenue`, `average_revenue`.
- **Pagination:** list endpoints return a `cursor`. Pass it back to get the next page. Page sizes are capped (`limit` ≤ 1000 for queries).
- **CSV:** stats and query endpoints return CSV with `Accept: text/csv` or `format=csv`.
- **Privacy:** responses never contain IP addresses, user agents or visitor IDs. Session IDs are per site and per day, so they can't follow a visitor across days.

## Stats

| Method | Path | Returns |
|---|---|---|
| GET | `/stats/summary?site_id&period` | Headline metrics, optionally compared with the previous period or year (`compare=previous_period`) |
| GET | `/stats/timeseries?site_id&period&interval` | Metrics per `hour`, `day`, `week` or `month`, with gaps filled |
| GET | `/stats/breakdown?site_id&period&dimension` | Top values of one dimension with chosen `metrics` |
| POST | `/query` | The general query: up to 3 dimensions × any metrics × filters, ordered and paginated |
| GET | `/realtime?site_id` | Visitors live now (last 5 minutes), plus the last 30 minutes per minute and by page, source, country, city, device and event, and a recent-activity feed |

### Traffic by weekday and hour

Use `weekday` and `hour_of_day` together to build a 7-by-24 traffic grid. Dimensions may be comma-separated or supplied as repeated `dimension` parameters; their order is preserved. Set `limit=168` to allow every possible cell, and request `visitors`, `pageviews`, or both.

```sh
curl "https://supermetry.com/api/public/v1/stats/breakdown?site_id=1&period=30d&dimension=weekday,hour_of_day&metrics=visitors,pageviews&limit=168" \
  -H "Authorization: Bearer <api-key>"
```

```json
{
  "columns": ["weekday", "hour_of_day", "visitors", "pageviews"],
  "rows": [
    { "weekday": "1", "hour_of_day": "9", "visitors": 42, "pageviews": 77 },
    { "weekday": "1", "hour_of_day": "10", "visitors": 35, "pageviews": 58 }
  ],
  "totals": { "visitors": 63, "pageviews": 135 },
  "meta": { "sources_used": ["hot"], "cached": false, "imported_included": false }
}
```

Weekdays are numbered `0` for Sunday through `6` for Saturday; hours run from `0` through `23`. Buckets are computed in the site's time zone. These dimensions are hot-tier only, so the whole date range must be inside the site's raw-retention window. The seven-day limit for `interval=hour` time series does not apply. A range that starts outside a 30-day retention window, for example, returns `400` with `Traffic breakdowns are available only within the site's 30-day raw retention window`.

## Reports

| Method | Path | Returns |
|---|---|---|
| POST | `/funnels/query` | Conversion through a saved or ad-hoc funnel |
| POST | `/journeys/query` | Page-to-page flow from a start page or into an end page |
| POST | `/retention/query` | Cohort retention (across days for identified profiles; same-day for anonymous visitors) |
| POST | `/heatmaps/query` | Click grid and scroll map for a page and device |
| POST | `/vitals/query` | Web Vitals (LCP, CLS, INP) overall/breakdown p75 and ratings plus retained daily p50/p75/p95 trends and sample counts, using site-timezone buckets |

Funnel steps are ordered within one site-local session. Page and custom-event values support `exact`, `prefix`, and anchored `wildcard` matching. In wildcard mode only `*` expands to any characters; `%` and `_` are literal. Exact and prefix modes also treat `*` literally. The wildcard grammar does not currently provide an escape for a literal `*`. Funnel `visitors` values count sessions completing each step, not durable people across sessions or days. Step property fields are reserved but are not yet applied by funnel queries.

## Explore raw data

| Method | Path | Returns |
|---|---|---|
| GET | `/sites/{site_id}/sessions` | Sessions, newest first: start, duration, pages, entry and exit, source, location, device |
| GET | `/sessions/{session_id}?site_id` | One session's pageviews and events in order |
| GET | `/sites/{site_id}/events` | Raw pageviews and custom events, newest first |
| GET | `/sites/{site_id}/properties` | Custom property keys with event counts |
| GET | `/sites/{site_id}/properties/{key}/values` | Top values of one property |
| GET | `/sites/{site_id}/suggestions?dimension&q` | Up to 20 matching values for a filter dimension |
| GET | `/sites/{site_id}/profiles` | Identified profiles (only for sites with profiles turned on) |
| GET | `/profiles/{profile_id}` | One profile's traits and event timeline |

Raw data (sessions timelines, events, properties) comes from the raw-event store, so it covers the site's raw retention window. Older ranges return `400` with an explanation.

## Revenue report

Dashboard session endpoints (not part of the public API-key surface):

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/private/sites/{site}/revenue/report` | Orders, revenue, average order value, revenue per visitor and, in identified mode with profiles, unique customers and repeat-purchase rate, by `breakdown` = `country`, `region`, `source`, `campaign`, `page` or `product` |
| GET | `/api/private/sites/{site}/revenue/customers` | Identified customers by revenue with search; needs `profiles:read` because external ids can be personal data |

Parameters: `from`, `to` (site-local dates), `breakdown`, `attribution` (`event`, `first_click`, `last_click`), `lookback_days` (1 to 90, default 30), `limit`. Send `Accept: text/csv` to export, which needs the `data:export` scope.

How the numbers are computed:

- **Orders** are events with a positive revenue amount; refunds and negative amounts are not modelled. Money is never added across currencies: a period with orders in more than one currency, or in a currency other than the site currency, returns `409`.
- **Event attribution** credits the source or campaign stored on the order. **First click** and **last click** look for earlier visits by the same person within `lookback_days` before the order. Visits without a source or campaign never take credit; an order with no qualifying visit is reported under the empty (direct) key. Ties on the same start time go to the lower session id. The window is inclusive at both ends.
- **Cross-day links** exist only through customer profiles. Visitor ids are re-salted daily, so without a profile a visit can match an order only on the same day.
- **Revenue per visitor** divides by daily visitors in the period (the same person on two days counts twice, as on the dashboard).
- **Customers** and the repeat rate (customers with two or more orders in the group, as a percentage) appear only in identified mode with profiles enabled.
- Orders come from retained raw events and sessions, so reports cover the site's retention window and are capped at 20,000 orders (`truncated`).
- The order query uses the partial `(site_id, ts)` index on custom events and the customer-touch query uses the `(site_id, profile_id, ts)` profile index, with partition pruning by time.

## Manage sites and definitions

| Resource | Endpoints | Scope for writes |
|---|---|---|
| Sites | `GET/POST /sites`, `GET/PATCH/DELETE /sites/{site_id}` | `sites:write` |
| Goals | `GET/POST /sites/{site_id}/goals`, `PATCH/DELETE /goals/{goal_id}` | `goals:write` |
| Funnels | `GET/POST /sites/{site_id}/funnels`, `PATCH/DELETE /funnels/{funnel_id}` | `goals:write` |
| Annotations | `GET/POST /sites/{site_id}/annotations`, `PATCH/DELETE /annotations/{annotation_id}` | `annotations:write` |
| Segments | `GET/POST /sites/{site_id}/segments`, `PATCH/DELETE /segments/{segment_id}` | `segments:write` |
| API keys | `GET/POST /api-keys`, `DELETE /api-keys/{key}` | `keys:manage` |

Reads need `stats:read`. Every write is recorded in the organization's audit log.

## Short links and tracking pixels

Short links redirect through your own domain and count clicks without storing IP addresses. The management endpoints belong to the dashboard session API (`/api/private`); viewers can read, editors and above can change.

| Endpoint | Purpose |
|---|---|
| `GET/POST /sites/{site}/links` | List and create links (a site holds at most 500) |
| `GET/PATCH/DELETE /links/{link}` | Read, change and delete one link |
| `GET /links/{link}/stats?from&to` | Clicks per day (site time zone), countries, referrer hosts and devices; range up to 366 days, default last 30 days |
| `GET /sites/{site}/pixel?campaign=` | Pixel image URL and HTML snippet |
| `GET /sites/{site}/pixel/stats?from&to` | Impressions per day and per campaign |

A link has a `slug` (2 to 64 lowercase letters, digits, `-` or `_`; reserved words such as `api` or `admin` are refused; generated when omitted), a `name`, a `destination`, optional default `utm_*` parameters, `enabled`, optional `expires_at` and optional `click_limit`. Destinations must be absolute `http` or `https` URLs of at most 2048 characters with no credentials, spaces or control characters, and may not point back at a short link. Changes are recorded in the audit log without the destination.

Public endpoints, no authentication:

- `GET /l/{slug}` answers `302` to the stored destination with the default UTM parameters appended unless the destination already sets them. The destination never comes from the request, so there is no open redirect. Disabled and unknown links answer `404`; expired links and links past their click limit answer `410`. Each client address is rate limited (`429` with `Retry-After`).
- `GET /px/{public_id}.gif?c=campaign` always answers with a 1x1 transparent GIF, whether or not the hit was counted. The optional `c` tag is up to 64 letters, digits, `.`, `_`, `:` or `-`; an invalid tag is ignored.

Bots, and visitors who send Do Not Track or Global Privacy Control on a site that respects them, are redirected but not counted. Counts are aggregated in memory and written in batches every few seconds and on shutdown, so a click appears in statistics after a short delay; click limits are enforced from the same buffer and can overshoot by at most one in-flight request per instance.

## Send events from your server

`POST /api/public/v1/events` accepts the same payload as the browser tracker, so you can record signups, purchases or API usage from your backend.

```sh
curl -X POST https://supermetry.com/api/public/v1/events \
  -H "Content-Type: application/json" \
  -H "User-Agent: $ORIGINAL_USER_AGENT" \
  -H "X-Forwarded-For: $ORIGINAL_CLIENT_IP" \
  -d '{"site":"<public-site-id>","name":"Purchase","url":"https://example.com/checkout","props":{"plan":"pro"},"revenue":{"amount":"49.90","currency":"USD"}}'
```

- Forward the visitor's user agent and IP so the event joins the right session and location. Supermetry parses them and never stores them. `X-Forwarded-For` is trusted only from addresses in `SUPERMETRY_TRUSTED_PROXIES`.
- The body is at most 8 KB, with up to 30 props.
- The endpoint answers `202 Accepted` and needs no API key; the site's allowed hosts and exclusion rules still apply.

## MCP for AI assistants

`POST /api/public/v1/mcp` speaks the Model Context Protocol (JSON-RPC 2.0, protocol 2025-06-18). It uses the same API key, needs `stats:read`, and sees only the key's sites. Its tools are:

- `list_sites`
- `get_summary`
- `get_timeseries`
- `get_breakdown`
- `get_realtime`
- `list_goals`
- `run_funnel`

Connection instructions for common clients are under **Organization settings → API keys** in the dashboard.

## Cohorts and power users

Saved audiences for identified sites with profiles enabled. Dashboard session endpoints (not part of the public API-key surface):

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/private/sites/{site}/cohorts` | Saved cohorts (any member of the site) |
| POST | `/api/private/sites/{site}/cohorts` | Save a cohort (owner, admin or editor; audited, up to 100 per site) |
| PATCH | `/api/private/cohorts/{cohort}` | Rename or redefine |
| DELETE | `/api/private/cohorts/{cohort}` | Delete |
| POST | `/api/private/cohorts/{cohort}/duplicate` | Copy as `<name> (copy)` |
| POST | `/api/private/sites/{site}/cohorts/evaluate` | Size, and optionally a sample, of a saved cohort (`cohort_id`) or an unsaved `definition` |
| GET | `/api/private/sites/{site}/power-users` | Top profiles by `metric` = `sessions` or `events` inside `from`..`to`, at least `min` (needs `profiles:read`) |

A cohort definition is `match` (`all` or `any`), `window_days` (1 to 90) and 1 to 10 conditions. Conditions: `event` (custom event `name`, at least `min_count` times), `page` (`path`, `operator` `is` or `starts_with`), `event_property` (event `name` with `key` equal to `value`), `activity` (at least `min_count` `metric` = `sessions` or `events`), `first_seen` and `last_seen` (`relation` `within` or `before` the last `days` days, up to 365). `negate` inverts any condition.

Rules worth knowing:

- Nothing person-level is stored: a cohort is only its conditions. Membership is computed on demand from retained events and profiles, so it changes with time and never reaches past the raw retention window.
- The window is rolling, in 24-hour days ending at evaluation time, and both edges are inclusive. `first_seen` and `last_seen` compare the profile timestamps with now minus `days`; `within` includes the cutoff instant and `before` excludes it.
- Counts stop at 100,000 (`size_capped`). Evaluation runs under the statement timeout and the shared query-slot limit; a definition that cannot finish in time answers `503`.
- The `evaluate` sample (up to 50 profiles with their external ids) is returned only to callers holding `profiles:read`; others get the size and `sample_withheld: true`. Power users always need `profiles:read`.
- Anonymous mode, or a site with profiles off, answers `403` with the policy explanation.
- A saved cohort works as a filter: `{"dimension": "cohort", "operator": "is", "values": ["<id>"]}` in `/api/private/query` and the sessions list. It matches sessions of people in the cohort evaluated at query time (sessions without a profile never match), bypasses result caching, and is not available for funnels, journeys or retention.
- `sessions` count distinct sessions with a pageview or custom event; `events` count pageviews and custom events. Engagement beacons never count.

## Custom dashboards

Saved, per-site dashboards built from a versioned widget layout. Dashboard session endpoints (not part of the public API-key surface); every path names the site, and an id that belongs to another site answers `404`:

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/private/sites/{site}/dashboards` | Saved dashboards by name, or the built-in dashboard alone (`builtin: true`, `id: 0`) when none exist (any member of the site) |
| POST | `/api/private/sites/{site}/dashboards` | Save one (owner, admin or editor; audited; up to 50 per site, names unique ignoring case) |
| GET | `/api/private/sites/{site}/dashboards/default` | The flagged default, else the built-in dashboard |
| DELETE | `/api/private/sites/{site}/dashboards/default` | Unflag the default (back to the built-in dashboard) |
| GET / PATCH / DELETE | `/api/private/sites/{site}/dashboards/{id}` | Read; rename or re-lay out (needs the current `version`, else `409`; bumps `version`); delete |
| POST | `/api/private/sites/{site}/dashboards/{id}/duplicate` | Copy as `<name> (copy)`; never the default |
| POST | `/api/private/sites/{site}/dashboards/{id}/default` | Make it the site default (one per site) |

Write access reuses the segment edit permission (keys need `segments:write`); viewers read only.

Layout (`schema_version` 1), at most 24 widgets and 32 KiB:

```jsonc
{
  "schema_version": 1,
  "period": "30d",              // optional: today yesterday 7d 28d 30d 91d 6mo 12mo month last_month year
  "compare": "off",             // optional: off previous_period previous_year
  "filters": [],                // optional, up to 10, same shape as query filters
  "widgets": [{
    "id": "pages",              // 1-40 chars of a-z 0-9 - _, unique in the layout
    "kind": "breakdown",
    "title": "Pages",           // optional, up to 80 chars
    "x": 0, "y": 7, "w": 6, "h": 5,   // 12-column grid: x+w <= 12, y < 200, h 1-12
    "period": "7d",             // optional override of the dashboard period
    "filters": [],              // optional, up to 10, added to the dashboard filters
    "config": { "dimension": "page", "metrics": ["visitors"], "limit": 10 }
  }]
}
```

Widget kinds and their `config` (fields a kind does not list are rejected; unknown JSON fields are rejected everywhere):

| kind | config |
|---|---|
| `kpi_strip` | `metrics` (1 to 6 known metrics) |
| `time_series` | `metric` (required) |
| `breakdown` | `dimension` (required; a known dimension or `prop:<key>`), `metrics` (1 to 4), `limit` (1 to 50) |
| `map`, `weekly_rhythm` | `metric` (`visitors` or `pageviews`) |
| `goals` | `limit` (1 to 50) |
| `funnel` | `funnel_id` (required) |
| `retention`, `web_vitals` | none |

Widgets only name existing capabilities; a layout never carries SQL. Overlapping widgets are accepted and the client reflows them. Metrics, dimensions and filters are checked with the same rules as `/api/private/query`.
## Integrations and Search Console

Connect a site to Google Search Console and read the imported queries and pages. Dashboard session endpoints (not part of the public API-key surface); the feature is hidden until `SUPERMETRY_GSC_CLIENT_ID` and `SUPERMETRY_GSC_CLIENT_SECRET` are set, and the OAuth redirect URI to register is `<SUPERMETRY_BASE_URL>/api/private/integrations/search-console/callback`.

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/private/sites/{site}/integrations` | Catalog of configured providers with each connection (owner, admin, editor). Never returns tokens |
| POST | `/api/private/sites/{site}/integrations/{provider}/connect` | Body `property` (for example `sc-domain:example.com`) and optional `backfill_days` (1 to 485, default 28). Returns `authorization_url` (owner or admin; audited; 10 per 10 minutes per user) |
| GET | `/api/private/integrations/{provider}/callback` | OAuth redirect target; consumes the one-time `state` and answers `303` to `/sites/{site}/settings/integrations?integration=...&status=connected\|denied\|error` |
| POST | `/api/private/sites/{site}/integrations/{provider}/backfill` | Body `days` (1 to 485); re-imports in bounded windows (owner or admin; audited; 5 per 10 minutes) |
| DELETE | `/api/private/sites/{site}/integrations/{provider}` | Disconnect: deletes the stored tokens and every imported row, revokes the grant (owner or admin; audited) |
| GET | `/api/private/sites/{site}/search-console/top` | `dimension` = `query` or `page`, `from`..`to` (at most 366 days), `limit` (1 to 500, default 50): clicks, impressions, CTR and impression-weighted position, plus totals (any site member except share links) |

Rules worth knowing:

- Tokens are sealed with the instance secret key and never logged or returned. The authorization `state` is random, bound to the user, organization and site, expires after 10 minutes and works once; PKCE (S256) is used.
- A scheduled job imports due connections every 30 minutes, at most 7 days per connection per run, up to two days behind today (the provider lags). The last 3 days are re-fetched on every run, and a day is replaced as a whole, so reruns never duplicate and revised numbers win.
- Failures back off (5 minutes doubling to 6 hours); a revoked grant parks the connection as `reauth` until the user reconnects.
- Storage keeps the top 5,000 rows per day, 490 days and 3,000,000 rows per site (oldest days are dropped first). Search queries are never shown through public share links.
## Imports (session endpoints, owner or admin)

| Method | Path | Notes |
|---|---|---|
| POST | `/api/private/sites/{site}/imports` | Multipart `source` (`plausible`, `umami` or `ga4`) and `file`; `?replace=true` replaces an earlier import of the identical file. Answers `202`; the import runs in the background. |
| POST | `/api/private/sites/{site}/imports/preview` | Same upload, dry run: parses with the import limits and stores nothing. Returns `rows`, `bad_rows`, `duplicate_rows`, up to 20 `errors` (`row`, `message`), `days`, `date_from`, `date_to`, `totals`, per-dimension `dimensions` counts and `overlap` (`imported_days`, `live_days`, `same_file`). |
| GET | `/api/private/sites/{site}/imports` | History, newest first. |
| DELETE | `/api/private/imports/{import}` | Undo: removes the imported batch. |

GA4 input is a CSV from the Data API, a GA4 report export (lines starting with `#` are ignored) or a BigQuery-style table. Needed: a `Date` (or `event_date`) column as `YYYYMMDD` or `YYYY-MM-DD`, and at least one of `Active users`, `Sessions`, `Views`, `Event count`. Optional breakdowns: page path, landing page, session source, country, region, city, device category, browser, operating system, event name. Dates are taken as the civil day already in the property's time zone. Bounce rate and average session duration are turned into bounced sessions and total visit seconds.

- A file with only a date and metrics gives exact daily totals. With breakdown columns, only additive metrics reach the daily totals (views always; sessions only without a page column), because users and per-page sessions do not add up across rows.
- Exact duplicate rows in one file are ignored and counted. Invalid rows are skipped with their line number. Limits: 512 MB per upload, 3,000,000 rows, 2,000,000 distinct day and value keys.
- Overlap: per day, the newest completed import wins and live data always wins, so re-importing an overlapping range never double counts. Undo restores the previous source for those days.
- Imported rows stay separate from live data and follow the existing `include_imported` query option.

## Operations endpoints

`GET /healthz` (process alive) and `GET /readyz` (ready to serve) are outside `/api` and need no key. `GET /metrics` (Prometheus) needs `Authorization: Bearer <SUPERMETRY_METRICS_TOKEN>` when that variable is set; without it, only clients on the same machine (loopback) can read it, and everyone else gets 404.
