# Migrate from Plausible

Move a site from Plausible (Cloud or Community Edition) to Supermetry in four steps: export, import, swap the script, recreate goals. Your Plausible history shows up in the same dashboard as new traffic, and nothing is lost if you need to undo.

## 1. Export from Plausible

In Plausible, open the site's **Site settings → Imports & Exports** and choose **Export to CSV**. You get a ZIP file of CSVs (Plausible Cloud emails a download link). Keep the ZIP as it is; do not unzip or rename the files inside.

## 2. Import into Supermetry

1. Create the site in Supermetry and set its **time zone** in Site settings → General to the same zone as in Plausible. Days are imported as Plausible wrote them.
2. Open **Site settings → Imports & export**, choose **Plausible**, pick the ZIP and upload. The import runs in the background; its status shows in the history list on the same page.
3. Charts include imported data when **Include imported data** is on in the dashboard.

What is imported, per day: visitors, visits, pageviews, bounces and visit duration, plus breakdowns by page, entry page, exit page, source, country, region, city, browser, operating system, device and custom event name.

Good to know:

- **No double counting.** On any day that already has live Supermetry data, live data wins. If two imports cover the same day, the newest wins. You can run the new script and the old one side by side, then import a fresh export afterwards.
- **Undo.** Each import in the history can be removed; live data is never touched.
- **Limits.** 512 MB per upload, 2,000,000 distinct day and value keys per import.
- **Not carried over:** custom event properties, revenue amounts, UTM medium and campaign breakdowns, and goal and funnel definitions. The source breakdown uses Plausible's source, falling back to `utm_source` and then the referrer.

To check a file before importing it, use the dry-run endpoint `POST /api/private/sites/{site}/imports/preview` with `source=plausible` (see [API: Imports](../api.md#imports-session-endpoints-owner-or-admin)). It reports rows, skipped rows, the date range and overlap with existing data, and stores nothing.

## 3. Replace the script

Copy the snippet from **Site settings → Installation**. It replaces the Plausible tag:

```html
<!-- Before -->
<script defer data-domain="example.com" src="https://plausible.io/js/script.js"></script>

<!-- After -->
<script defer data-site="YOUR_SITE_ID" src="https://YOUR_SUPERMETRY_HOST/js/script.js"></script>
```

Plausible's script variants (`script.outbound-links.js`, `script.file-downloads.js`, `script.hash.js`, `script.tagged-events.js` and so on) become switches in **Site settings → Tracking**. Supermetry builds one script with the options you turn on, so the URL never changes.

| Plausible | Supermetry |
|---|---|
| `plausible('Signup')` | `supermetry.track('Signup')` |
| `plausible('Signup', {props: {plan: 'pro'}})` | `supermetry.track('Signup', {plan: 'pro'})` |
| `plausible('Purchase', {revenue: {amount: 49.9, currency: 'USD'}})` | `supermetry.track('Purchase', {}, {revenue: {amount: 49.9, currency: 'USD'}})` |
| `class="plausible-event-name=Signup plausible-event-plan=pro"` | `class="supermetry-event-Signup supermetry-event-plan=pro"`, or `data-supermetry-event="Signup" data-supermetry-prop-plan="pro"` |
| `script.manual.js` + `plausible('pageview', {u: url})` | `data-manual="true"` on the tag + `supermetry.pageview({url})` |
| `localStorage.plausible_ignore = 'true'` | `supermetry.optOut()` |
| 404 tracking with `plausible('404')` | Turn on **Track 404 pages** and add `<meta name="supermetry:404">` to your 404 template |

Tagged elements (CSS classes or `data-supermetry-event`) are tracked when at least one switch in **Site settings → Tracking** is on, for example **Track outbound links**. In CSS class names, write a space in a value as `+`, as in Plausible.

If you proxied Plausible to avoid ad blockers, point the same proxy at Supermetry; [Ad blockers](ad-blockers.md) has ready-made nginx, Caddy, Cloudflare Worker and Next.js recipes.

## 4. Recreate goals and funnels

Goals, funnels and revenue goals are configuration, not data, and are not part of the export. Add them in **Site settings → Goals** and **Site settings → Funnels** using the same event names, and imported custom event counts line up with them.

## Checklist

- [ ] Time zone matches Plausible
- [ ] Plausible ZIP imported and visible with **Include imported data** on
- [ ] Script replaced on every page and Tracking switches set
- [ ] `plausible(...)` calls and `plausible-event-*` classes replaced
- [ ] Goals and funnels recreated
- [ ] After a day or two of overlap, remove the Plausible script
