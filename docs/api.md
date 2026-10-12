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

## Operations endpoints

`GET /healthz` (process alive) and `GET /readyz` (ready to serve) are outside `/api` and need no key. `GET /metrics` (Prometheus) needs `Authorization: Bearer <SUPERMETRY_METRICS_TOKEN>` when that variable is set; without it, only clients on the same machine (loopback) can read it, and everyone else gets 404.
