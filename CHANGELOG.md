# Changelog

All notable changes to Supermetry are listed here, newest release first.

Supermetry is a self-hosted web analytics product: one Go binary and PostgreSQL, no ClickHouse, no Kafka, no Node in production.

## Unreleased

### Added

- Instance admin API additions: `SUPERMETRY_ADMIN_API_KEY` (32+ characters) as a bearer token for `/api/private/admin/*`; admin delete of an organization (restorable for 7 days); admin change of a site's `retention_days`; admin creation of users with an optional organization and reset link; and time-limited, audited "sign in as a user" support sessions.
- Pixel analytics projects ("Views anywhere"): 1×1 GIF pixels and SVG view-count badges for GitHub profiles, READMEs and docs. Views are daily aggregates split by image proxy and direct; no IPs or user agents are stored. Up to 100 pixels per project.
- Badges carry a small Supermetry mark; set `SUPERMETRY_BADGE_BRANDING=false` to remove it. `?style=flat|flat-square` and `?color=<hex>` customise them.
- Open counters (`/c/github.com/{owner}[/{repo}].svg`), off by default: enable with `SUPERMETRY_OPEN_COUNTERS=true`; optional `SUPERMETRY_OPEN_COUNTER_BLOCKLIST` file, one key per line.

### Security and upgrade notes

- **Existing `SUPERMETRY_ADMIN_API_KEY` values gain power.** A key of 32 or more characters now also grants instance-admin API access (`/api/private/admin/*`), including deleting organizations, changing retention, creating users and signing in as a user. If that is not wanted, rotate the key or unset it before upgrading. Sessions already started with the key last up to 1 hour after rotation.
- Organization audit entries written during a "sign in as a user" session carry `impersonated_by` with the admin's user id.

## v0.1.0 (2026-10-14)

### Analytics and metrics
- Visitors, pageviews, bounce rate, visit duration, time on page and exit rate, with comparison against the previous period or year and periods from today to 12 months.
- Filters on any dimension, saved segments, annotations on the chart, goals, funnels, user journeys and a retention report.
- Breakdowns by page, entry and exit page, source, UTM fields, country, region, city, browser, operating system and device, plus a weekday by hour traffic grid in the site time zone.
- Sessions explorer with a per-session timeline, and an events and properties explorer.
- Cohorts and power users: save audience definitions (events, pages, properties, activity, first or last seen), see sizes on demand, use a cohort as a dashboard filter, and rank top profiles.
- Web Vitals: LCP, CLS, INP, FCP and TTFB with thresholds, trends and breakdowns by page and device.
- Heatmaps (aggregated clicks and scroll depth) and user profiles with `identify`, both opt-in per site.

### Dashboard and UI
- A sites overview with cards or a table, search, sorting, periods, live visitors and install status; a landing preference (overview or last site).
- A world map and weekly rhythm cards on the dashboard, a styled date range picker and a mobile filter sheet.
- Custom dashboards: saved per-site layouts of nine widget kinds, a switcher, an editor with add, remove, reorder and resize, per-dashboard period and filters, and a default dashboard.
- A "View full report" dialog on breakdown cards with search, sort and export to CSV or JSON with column choice.
- Install recipes for HTML, React, Next.js, Vue, Nuxt, SvelteKit, Astro, Remix, Angular, WordPress, Shopify, Google Tag Manager, curl, Node.js, Python and Go, with a "verify installation" check.

### Realtime and accuracy
- The live visitor count is the number of distinct visitors with an accepted pageview or custom event in the last five minutes. Engagement and Web Vitals pings no longer keep a visitor online, which fixes a 20 to 30 percent overcount.

### Revenue
- Revenue goals with a project currency, and a revenue report: orders, average order value, revenue per visitor, repeat purchases and unique customers (identified mode), broken down by country, region, source, campaign, page or product.
- First-click, last-click and event attribution with a 1 to 90 day lookback, a customers table and CSV export. Orders in more than one currency are refused.

### Sessions and replay
- Session replay with a responsive contain-fit player, triage filters (search, duration, events, trigger), saved recordings and card or table layouts.

### Privacy and security
- No cookies. Identified tracking is the default mode and anonymous mode is available with `SUPERMETRY_TRACKING_MODE=anonymous`; raw IPs and full user agents are never stored, and salts expire after about 48 hours.
- Do Not Track and Global Privacy Control are respected by default, with a visitor opt-out.
- Two-factor authentication (TOTP) with an instance-wide policy (nobody, instance admins, owners and admins, or everyone) with a grace period and audited exemptions.
- Security headers per route class, opt-in embeds for share links, configurable public share links (sections, locked scope, date range, theme), and an audit log.

### Admin and organizations
- Organizations with roles and per-site access, invitations, account self-service (name, email, password, delete account), and an instance admin console for users, organizations, sites, settings and security.
- Notification alerts (traffic spike or drop, goal hit) by email, Slack, Discord, Telegram or webhook, with delivery history, retries and editable message templates.

### Integrations and importers
- File importers for Plausible, Umami, GA4, Mixpanel and Amplitude with a dry-run preview, per-day de-duplication, row-numbered errors, history and undo.
- Google Search Console: top queries and pages per site (OAuth with sealed tokens, daily sync; disconnect deletes tokens and data). The feature appears when `SUPERMETRY_GSC_CLIENT_ID` and `SUPERMETRY_GSC_CLIENT_SECRET` are set.
- Short links and tracking pixels with default UTM tags, expiry, click limits, a QR code and per-link statistics.

### API
- Stats API (summary, timeseries, breakdowns, realtime), a sessions and events API, API keys with scopes, a typed client, an in-app reference and an MCP server for AI assistants.
- A server-side events API, and private dashboard endpoints for revenue, cohorts, links, dashboards and integrations (documented in `docs/api.md`).

### Installer and operations
- A guided installer for Linux, macOS and Windows (`install.sh`, `install.ps1`) for Docker or binary installs, with a dry-run plan, PostgreSQL checks, nginx or Caddy configuration and a systemd unit.
- Release binaries for eight OS and architecture targets with `SHA256SUMS`, a multi-arch container image, backup and restore commands, retention by partition drop, and a Prometheus metrics endpoint.
- Update notifications for instance admins, and admin-approved one-click updates for binary and Docker installs, verified against an Ed25519-signed release manifest.

### Localization
- English, plus machine-assisted first translations in Spanish and German (marked as such in the language picker). Dates, numbers, relative time and the first day of the week follow the language. Some text is not translated yet.

### Performance
- A lazy-loaded dashboard with an initial JavaScript budget (about 224 KB gzip today), startup in about 160 ms and an idle memory of about 90 MB.

### Accessibility
- Skip link, grouped sidebar navigation, labelled controls, 44 px touch targets on the new screens, text summaries for charts and the map, and axe checks on login and the dashboard.

### Known limits
- Spanish and German have not been reviewed by native speakers.
- Some interface text outside the message catalogue, such as several card titles, is English only.
- Click limits on short links are enforced per instance from an in-memory buffer and can overshoot slightly.
- The Google Search Console integration has been tested against a fake provider, not the live Google API.
