# Migrate from Umami

Move a site from Umami to Supermetry in four steps: export events, import, swap the script, recreate goals and funnels. Umami history shows up in the same dashboard as new traffic, and every import can be undone.

## 1. Export from Umami

Supermetry reads a CSV of Umami's `website_event` rows. Each row needs `session_id`, `created_at` and `event_type`. These columns are optional but fill the breakdowns: `url_path`, `event_name`, `referrer_domain`, `country`, `browser`, `os` and `device`. The last four live in Umami's `session` table, so the export joins the two tables.

**Self-hosted Umami on PostgreSQL** (Umami 2 and 3). Find the website ID in Umami under **Settings → Websites → Edit**, then run:

```sh
psql "$UMAMI_DATABASE_URL" -c "\copy (
  select e.session_id, e.created_at, e.event_type, e.event_name, e.url_path,
         e.referrer_domain, s.country, s.browser, s.os, s.device
  from website_event e
  join session s on s.session_id = e.session_id
  where e.website_id = 'YOUR_UMAMI_WEBSITE_ID'
    and e.event_type in (1, 2)
  order by e.created_at
) to 'umami-events.csv' with (format csv, header)"
```

`event_type` 1 is a pageview and 2 a custom event; other types (such as session data) are not analytics traffic and would be skipped as invalid rows.

**Umami on MySQL** (Umami 2): run the same `select` in your MySQL client and save the result as CSV with a header row. Keep `created_at` in UTC, formatted like `2026-01-31 14:05:09`.

**Umami Cloud:** use a CSV export of website events if your plan offers one, and check that it has at least the three required columns.

## 2. Import into Supermetry

1. Create the site in Supermetry and set its **time zone** in **Site settings → General**. Umami stores UTC timestamps; Supermetry buckets them into days in the site's time zone, so set the zone before importing.
2. Open **Site settings → Imports & export**, choose **Umami**, pick the CSV and upload. The import runs in the background; its status shows in the history list on the same page.
3. Charts include imported data when **Include imported data** is on in the dashboard.

What is imported, per day: visitors, visits, pageviews, bounces and visit duration, plus breakdowns by page, entry page, exit page, source (referrer domain), country, browser, operating system, device and custom event name. Umami's export has no stable visitor ID, so visitors and visits per day are both counted as distinct Umami sessions that day.

Good to know:

- **No double counting.** On any day that already has live Supermetry data, live data wins. If two imports cover the same day, the newest one wins for that whole day.
- **Large sites.** One upload can be up to 512 MB with up to 2,000,000 session-days. To go further, split the export by date (for example one file per year), and split on whole days in the site's time zone. Because the newest import wins per day, a day split across two files would keep only the second half.
- **Undo.** Each import in the history can be removed; live data is never touched.
- **Not carried over:** event properties (`event_data`), UTM breakdowns, regions and cities, revenue, and goal, funnel or report definitions.

To check a file before importing it, use the dry-run endpoint `POST /api/private/sites/{site}/imports/preview` with `source=umami` (see [API: Imports](../api.md#imports-session-endpoints-owner-or-admin)). It reports rows, skipped rows with line numbers, the date range and overlap, and stores nothing.

## 3. Replace the script

Copy the snippet from **Site settings → Installation**. It replaces the Umami tag:

```html
<!-- Before -->
<script defer src="https://cloud.umami.is/script.js" data-website-id="UMAMI_WEBSITE_ID"></script>

<!-- After -->
<script defer data-site="YOUR_SITE_ID" src="https://YOUR_SUPERMETRY_HOST/js/script.js"></script>
```

| Umami | Supermetry |
|---|---|
| `umami.track('Signup')` | `supermetry.track('Signup')` |
| `umami.track('Signup', {plan: 'pro'})` | `supermetry.track('Signup', {plan: 'pro'})` |
| `umami.track('Purchase', {revenue: 49.9, currency: 'USD'})` | `supermetry.track('Purchase', {}, {revenue: {amount: 49.9, currency: 'USD'}})` |
| `data-umami-event="Signup" data-umami-event-plan="pro"` | `data-supermetry-event="Signup" data-supermetry-prop-plan="pro"` |
| `umami.identify('user-42', {plan: 'pro'})` | `supermetry.identify('user-42', {plan: 'pro'})`, on every page load (string trait values only) |
| `data-auto-track="false"` + `umami.track(props => ({...props, url}))` | `data-manual="true"` + `supermetry.pageview({url})` |
| `data-exclude-search="true"` | Not needed: query strings are always dropped |
| `data-do-not-track="true"` | Not needed: Do Not Track and Global Privacy Control are always honoured |
| `localStorage.umami.disabled = 1` | `supermetry.optOut()` |

Notes:

- `data-supermetry-event` elements are tracked when at least one switch in **Site settings → Tracking** is on, for example **Track outbound links**. The same page has switches for file downloads, form submissions, 404 pages, hash routes and Web Vitals.
- `identify` works in projects with profiles turned on (the default for new projects); see [Tracking and privacy](../tracking-privacy.md).
- Session replay and heatmaps are separate per-project opt-ins.
- If you proxied Umami to avoid ad blockers, point the same proxy at Supermetry; see [Ad blockers](ad-blockers.md).

## 4. Recreate goals and funnels

Umami goals, funnels, journeys and saved reports are not part of the event export. Add goals and funnels in **Site settings → Goals** and **Site settings → Funnels** using the same event names, so imported event counts line up with them. User journeys and retention need no setup.

## Checklist

- [ ] Site time zone set before importing
- [ ] Events CSV imported and visible with **Include imported data** on
- [ ] Script replaced on every page and Tracking switches set
- [ ] `umami.track(...)` calls and `data-umami-event` attributes replaced
- [ ] Goals and funnels recreated
- [ ] After a day or two of overlap, remove the Umami script
