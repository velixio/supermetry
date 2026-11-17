# Changelog

All notable changes to Supermetry are listed here, newest release first.

Supermetry is a self-hosted web analytics product: one Go binary and PostgreSQL, no ClickHouse, no Kafka, no Node in production.

## Unreleased

### Added

- App issues can be merged into one another and split apart again by fingerprint. Events, daily and hourly counts move with the merge, new events with a merged fingerprint land on the kept issue, and unmerging recomputes both issues from their events.
- App issues can be assigned to a project member, given a priority (high, medium, low), snoozed, archived until they escalate, and linked to a GitHub issue. Team members can comment, and every change, regression and first sighting appears on the issue's activity timeline. The site export gains `app_issue_activity.csv` and `app_issue_comments.csv`.
- Snoozed app issues wake when their time, event or user limit is reached, and archived issues escalate back to open on a sudden spike (more than 10 events an hour and 5 times the usual rate). Assignees get an email when an issue is assigned to them, regresses or is resolved by someone else, with per-project switches under Account, Notifications.

## v0.1.26 (2026-11-18)

### Fixed

- The Sentry intake's per-IP limiter no longer rescans its whole table for every new client address once it is full; envelope throughput rose about fivefold in the load test.
- A project over its event budget now answers 429 with `X-Sentry-Rate-Limits` for errors awaiting symbolication too, so SDKs back off.
- A failed "email address changed" notice is now logged, and the deletion-notice log names the SMTP step that failed (never the recipient address).
- Umami imports accept PostgreSQL's default timestamp text (`2026-01-31 14:05:09.123+00`), which is what `psql \copy` writes. Such rows were previously skipped as invalid.

### Added

- **App Monitoring data in the site export.** The ZIP export of an app project now also contains `app_issues.csv`, `app_events.csv`, `app_releases.csv`, `app_deploys.csv`, `app_artifacts.csv` and `app_client_reports.csv` (source map bundle metadata only), within the chosen date range. User identifiers, install and session hashes, IP data, event bodies and file contents are never exported. Web and pixel project exports are unchanged.
- **Source map upload tested against the real tools.** `make sdk-compat-sourcemaps` builds a Vite app and uploads its source maps with `sentry-cli`, `@sentry/vite-plugin` and the TypeScript `sentry` CLI against a fresh server. Upload tokens now always end in a character the Rust `sentry-cli` accepts (about three in four earlier tokens were rejected). Guide: [Upload source maps](docs/guide/source-maps.md).
- Sentry SDK compatibility: point an existing Sentry SDK at Supermetry by changing the DSN.
- **Source maps for JavaScript errors.** A Sentry-compatible upload API at `/api/0/` (chunk upload, artifact bundles, releases, deploys) works with unmodified `sentry-cli`, `@sentry/vite-plugin` and the `sentry` CLI using only `SENTRY_AUTH_TOKEN` and `SENTRY_PROJECT`. Minified stack frames are resolved to the original file, line and context by debug id, or by release and URL. New **upload tokens** (Installation page) carry only the `releases:write` scope. See [Source maps](docs/guide/source-maps.md).
- **Stored error details.** Each error keeps its scrubbed event body (stack frames, breadcrumbs, context) for 30 days for the event detail view; emails, IPs, cookies, auth headers and home paths are removed before storage. SDK client reports are kept as daily counts of discarded events.
- **Inbound filters** drop errors from browser extensions and crawlers (on by default) and from localhost (off by default, `SUPERMETRY_SENTRY_FILTER_LOCALHOST`), with per-project switches in app settings.
- **Erase an app user's data** from app settings or `POST /api/private/sites/{site}/app/users/erase`: removes their stored event bodies and the user hash on their events.
- Crash-free sessions count aggregated session pings by their occurrences, and every distinct grouping hash of an issue is recorded.
- Limits for the Sentry endpoints: per-IP and per-token rate limits on `/api/0/` (`SUPERMETRY_SENTRY_API_IP_PER_MIN`, `SUPERMETRY_SENTRY_API_TOKEN_PER_MIN`), a per-organization source map storage cap (`SUPERMETRY_SENTRY_ORG_CAP`, default 5 GB) and a daily sweep of unused upload chunks. Symbolication runs off the request path (`SUPERMETRY_SYMBOLICATE_WORKERS`, `SUPERMETRY_SYMBOLICATE_CACHE_MB`).
- **App Monitoring setup screen.** An app project's Installation page now shows the project DSN with a copy button and ready-to-paste snippets for the Sentry SDKs for JavaScript (browser), Node, Electron, Python, Go, Android and Cocoa; only `dsn` and `release` are set. DSN keys can be created with allowed origins and revoked there, and the TypeScript and Python kits move under "Legacy kits". Issues link to a full event detail view (exception chain with in-app frames, breadcrumbs, contexts, tags), served by `GET /api/private/sites/{site}/app/events/{event_id}`. The sidebar section for app projects is named "App Monitoring". Guides: [App Monitoring](docs/guide/apps.md) and [Migrate from Sentry](docs/guide/migrate-from-sentry.md).
- **Email (SMTP) in the app.** Instance admins can set up outgoing email under Instance admin, Email (SMTP) (host, port, connection security, username, password, from, HELO name) and send a test email that reports the failing step. The settings live in the database with the password encrypted, apply without a restart, and the password is never shown again (`GET/PUT/DELETE /api/private/admin/email`, `POST /api/private/admin/email/test`). When `SUPERMETRY_SMTP_HOST` is set, the environment wins entirely and the screen is read-only. Not available on managed instances.
- **Implicit TLS (SMTPS, port 465)** and the `SUPERMETRY_SMTP_TLS` variable (`starttls`, `implicit`, `none`). Without it, `SUPERMETRY_SMTP_STARTTLS` still applies, and port 465 with neither set uses implicit TLS. Certificates are always verified. A username without TLS is now rejected at startup instead of at the first send.

- Five read-only **App Health tools** for AI assistants on the MCP server: `get_app_overview`, `list_app_issues`, `get_app_issue`, `get_app_compatibility` and `list_app_releases`. They use the same API key, `stats:read` scope and site restrictions as the other tools, work in every tracking mode, and never return install or session hashes. The web analytics tools now return a clear error on an app project, and the app tools on a web or pixel project.
- Guides: [Migrate from Plausible](docs/guide/migrate-from-plausible.md) and [Migrate from Umami](docs/guide/migrate-from-umami.md). They cover exporting, importing, mapping the tracking calls and attributes, and what does not carry over. The Umami guide includes a ready-to-run export query.
- A Helm chart in `deploy/helm/supermetry`. It runs Supermetry with a bundled PostgreSQL 18 StatefulSet or an external database, generates the secret key once and keeps it across upgrades, and supports ingress with TLS, a persistent data volume and the `restricted` Pod Security Standard. `helm test` checks `/healthz`.
- One-click deploy templates for Railway (`railway.json`), Render (`render.yaml`), DigitalOcean App Platform (`.do/deploy.template.yaml`) and Coolify (`deploy/coolify/docker-compose.yml`), with deploy buttons in the README. See [One-click deploy](deploy/README.md#one-click-deploy).
- Platform integrations:
  - [WordPress](https://github.com/velixio/supermetry-wordpress): a `supermetry` plugin (GPL-2.0-or-later, in its own repository) with a Settings → Supermetry page, role exclusions, tracker options, an optional first-party proxy through the WordPress REST API, and WooCommerce `Purchase` events with revenue, sent once per order.
  - [Shopify](sdk/shopify): a Shopify CLI app with a theme app embed that loads the tracker and a web pixel that sends `Product Viewed`, `Add to Cart`, `Checkout Started` and `Purchase` (with revenue, deduplicated per order) from checkout. Stores without an app can paste a theme snippet and a custom pixel.
  - [Ghost](sdk/ghost): a code injection snippet and a theme partial that also send `Signup` and `Subscription` events after Portal signups and Stripe checkouts.
- Stripe revenue. Enable Stripe for a site in Settings → Integrations, add the shown webhook URL in Stripe and paste its signing secret. Checkout purchases and subscription renewals are recorded as revenue (`Stripe: Purchase`, `Stripe: Renewal`), attributed to the visit's source and campaign when the Checkout Session carries the id from the new `attributionId()` of `@supermetry/tracker` as `client_reference_id`. Refunds are listed on the Stripe card. No Stripe API key and no cookies; no customer names, emails or addresses are stored. See [the Stripe guide](docs/guide/stripe.md).

### Security

- Updated `github.com/andybalholm/brotli` to v1.2.7 (GO-2026-6888).

## v0.1.25 (2026-11-16)

### Added

- An **Installation** page for app projects (Settings, Installation). It shows the app ID and host with copy buttons, the current ingest key prefixes (with the end date of a rotated key), setup code for the TypeScript and Python kits and for plain HTTP with the app's values filled in, and the check that waits for the first report. Before, the app ID and setup steps appeared only once, in the dialog shown when the app was created or its key rotated.
- The "Set up this app" link on an app's empty dashboard opens the new Installation page.

## v0.1.24 (2026-11-16)

### Security

- Built with Go 1.27.4 and `klauspost/compress` v1.20.2, which fix GO-2026-6865 (`net/http`) and GO-2026-6859.

### Fixed

- The TypeScript and Python client kit READMEs show the real ingest key prefix, `smk_`, instead of `sk_app_`.

## v0.1.23 (2026-11-16)

### Added

- **App health** for desktop, mobile and backend apps. A new project type, **App**, on the new site page and in onboarding. Creating one shows a write-only ingest key once, with setup steps and a check that waits for the first test report. Keys can be rotated from the app's settings; the old key keeps working for 7 days.
- App settings: a tag allowlist, a maximum report age (1 to 30 days, default 7) and an environments list.
- App dashboard: crash-free users and crash-free sessions, events, crashes, new and regressed issues; an issues list with status tabs, search and sorting; issue detail with breakdowns by release, platform, OS, architecture, device class, environment and tags, and the latest scrubbed stack frames. Issues can be resolved, resolved in the next release, ignored or reopened.
- A compatibility matrix by platform, OS version, architecture and device class, and a releases page with crash-free rates and new and regressed issues per release.
- Four app alerts: new issue, regressed issue, crash-free rate below a threshold, and issue spike. They use the existing notification channels.
- Client kits under `sdk/` (MIT): TypeScript (Node 18+, Electron, Bun, Deno, browsers) and Python (3.9+), with no runtime dependencies. They never send exception messages.
- A guide, [Desktop and mobile apps](docs/guide/apps.md).
- Late events now count as **Too old** drops on the Data health page instead of disappearing silently.
- An app events endpoint, `POST /api/public/v1/app-events`, for crashes, errors, hangs and install failures. It takes up to 20 events per request with an app ingest key, scrubs home folders and absolute paths from stack frames, groups events into issues by fingerprint, tracks regressions of resolved issues, and stores install and session ids only as HMACs. The reply lists every dropped event with a reason, and drops show on the Data health page.
- A web site can send its `diagnostic_report` events to an app project (Settings, General). They are stored as app events with the app's report age limit instead of the 24-hour rule.

### Fixed

- The tracker sends events with `mode: "no-cors"`. Like the old beacons, it now follows a redirect that has no CORS headers (for example from a first-party proxy) instead of dropping the event.
- The ad blocker proxy recipes in Settings → Installation use the instance's script directory. With a custom `SUPERMETRY_TRACKER_PATH`, the first-party snippet and the forwarded paths now match. The self-hosted note also mentions forwarding a changed `SUPERMETRY_EVENT_PATH`.

## v0.1.22 (2026-11-03)

### Changed

- The tracker sends events with `fetch` (`keepalive`) instead of `navigator.sendBeacon`. Blockers file beacons as "ping" requests, which many filter lists block outright. Events still arrive when the page closes, and there is no extra preflight request.

### Added

- An "Ad blockers" section in Settings → Installation. It explains what the tracker already does and has ready-to-copy first-party proxy configurations for nginx, Caddy, a Cloudflare Worker and Next.js, filled in with the site's ID and host. On Supermetry Cloud it links to the site's proxy key and includes the key headers. Self-hosted, it explains `SUPERMETRY_TRUSTED_PROXIES`. It also offers the first-party snippet and points to server-side events.
- A guide, [Ad blockers](docs/guide/ad-blockers.md), linked from the README.

## v0.1.21 (2026-11-03)

Same code as v0.1.20.

## v0.1.20 (2026-11-03)

### Added

- Neutral per-site intake paths to get past ad blocker rules for analytics URLs. Events go to `POST /e/<site>`. Replay uses `/e/<site>/r.js` for the recorder, `/e/<site>/s` to start, `/e/<site>/c` for chunks and `/e/<site>/k/<hash>` for stylesheets. A site's script uses them when it is loaded straight from `SUPERMETRY_BASE_URL`, and keeps the classic paths behind a first-party proxy. Requests whose body or `?s=` names another site are rejected. The classic paths are unchanged. `SUPERMETRY_NEUTRAL_PATHS=false` turns this off.

### Changed

- The tracker's lazy-module loader requests `?m=v,h,r` instead of `?module=vitals,heatmap,replay`. The long form is still accepted.

## v0.1.19 (2026-10-28)

### Changed

- On managed instances (`SUPERMETRY_MANAGED=true`) a site's host must now look like a domain: it needs a dot (`example.com`, `blog.example.co.uk`, `*.example.com`). `localhost` and IP addresses are still accepted for testing, and other single-label hosts such as `afaridar`, which the tracker can never match, are rejected with 400 (`host "afaridar" is not a domain: use something like example.com`). The new-site form and the site settings show the same hint. Self-hosted instances are unchanged and keep intranet names like `wiki`. Existing sites are untouched, and saving a site that already stores a single-label host keeps working.

## v0.1.18 (2026-10-28)

### Added

- Welcome email, sent once in the background after an account is created by signup or by accepting an invitation. It never delays the request and is skipped when SMTP is not configured.
- `/email-logo.png` and `/email-logo-light.png` (for dark mode), the public logo images used by emails, in `web/public/`.
- Email preview: `SUPERMETRY_EMAIL_PREVIEW_DIR=/tmp/emails go test ./internal/platform/mail -run Preview` writes every email as HTML and text. See `docs/guide/notifications.md`.

### Changed

- Every email now uses one branded layout (gradient brand bar, larger logo, small label above the heading, details panel, button with the full link under it, footer with Dashboard, Docs and Privacy links, real dark mode with a light logo variant) in HTML and plain text: password reset, invitation, confirm new email, email changed, deletion notices and scheduled reports. Invitations now name the organization, who invited, the role and the expiry. Alert emails stay plain text.
- SMTP message parts are sent quoted-printable, so long HTML lines never exceed the SMTP line limit.

## v0.1.17 (2026-10-27)

### Fixed

- Email from Supermetry (password reset, invitations, notices) no longer waits about a minute at some mail providers. The SMTP client announced itself as `localhost`; it now announces a real host name (the host of `SUPERMETRY_BASE_URL`, or the value of the new `SUPERMETRY_SMTP_HELO`), and every message carries `Date` and `Message-ID` headers.

### Added

- Per-organization replay retention cap for instances run by an operator: `GET`, `PUT` and `DELETE /api/private/admin/organizations/{org}/replay-retention-cap` (instance admin only). A site's effective replay retention is the smallest of the instance cap, the organization's cap and the site's own setting. Lowering a cap deletes older recordings at the next cleanup run. Without a cap, behavior is unchanged.
- `DELETE /api/private/admin/users/{user}` (instance admin only): delete an account without the user's password, optionally erasing the organizations they solely own at once. The same address can sign up again afterwards.
- `SUPERMETRY_MANAGED=true` (default `false`) marks an instance run by an operator: the dashboard hides the instance administration links and shows a calm "managed by its operator" page at `/app/admin/*`, and in-app update check and apply answer 409. Authorization and the rest of the admin API are unchanged. `GET /api/private/auth/config` reports it as `managed`.
- `POST /api/private/admin/organizations/{org}/erase` (instance admin only): erase an organization and its data now instead of waiting for the retention period; the request body repeats the slug to confirm.
- `SUPERMETRY_SMTP_HELO` (optional): the host name announced in the SMTP greeting.
- GitHub and brand images for the repository page in `docs/brand/github/`.

### Upgrade notes

- Two new migrations, `0250_org_replay_retention_cap.sql` and `0251_org_erase_requested.sql`, each adding a nullable column to `orgs`; nothing changes until the features are used.

## v0.1.1 to v0.1.16 (accumulated)

The notes below were collected under "Unreleased" across these releases and were not split per release.

### Fixed

- Email from Supermetry (password reset, invitations, notices) no longer waits about a minute at some mail providers: the SMTP client now announces a real host name instead of `localhost` (the host of `SUPERMETRY_BASE_URL`, or set `SUPERMETRY_SMTP_HELO`), and every message carries `Date` and `Message-ID` headers.
- Per-site tracking scripts served through a first-party proxy under a path prefix (for example `/sm/js/<site>.js`) now send events under the same prefix (`/sm/api/public/v1/events`) without needing `data-api`.
- Session replay behind a first-party proxy under a path prefix: the tracker now loads `replay.js` and uploads recordings next to `data-api` (for example `/sm/replay.js`), not at the site's root, where they returned 404. The SDK proxy handlers (`@supermetry/tracker/proxy`, Next.js, Remix) now forward `/replay.js` and the replay uploads too.
- Password reset emails now link to the dashboard's `/reset/{token}` page; the old `/password-reset?token=` link pointed at a missing page and now redirects there.

### Added

- `proxyKey` option for the SDK proxy handlers (`@supermetry/tracker/proxy`, Next.js, Remix): on Supermetry Cloud, tracking requests carry the site's proxy key and the visitor's address so visitors keep their real location and identity.
- `SUPERMETRY_DEFAULT_RETENTION_DAYS` (7, 30, 90, 365 or 0; default 30) sets the retention a new site gets when none is chosen, and `SUPERMETRY_RETENTION_FOREVER` (default `true`) can turn off "keep forever": the API then rejects `retention_days: 0` with 400 and the dashboard hides the option. `SUPERMETRY_SUPPORT_URL` adds an optional "Contact support" link to the sign-in and error pages. All three are reported by `GET /api/private/auth/config`.
- Account recovery and onboarding: a "Forgot password?" page, clearer reset and invitation pages (an invitation names the organization and inviter, a signed-in invitee joins with one button, a new account is signed in after accepting), a two-field first-site form (domain and time zone, with retention and revenue currency under Advanced options), a live installation status with troubleshooting, and a no-data banner that follows the site's install status.
- Members: remove a member, leave an organization (`DELETE /api/private/orgs/{org}/membership`; the last owner gets 409), confirmations before reducing access, and invitation status, expiry, resend and confirmed revoke. `GET /api/private/invitations/{token}` previews an invitation without using it.
- Instance admin pages: create users, sign in as a user, delete an organization and edit a site's retention; the support-session bar stays in view and shows when the session ends (`impersonation_expires_at` on `GET /api/private/me`).
- Branded 404, error and unknown-site pages.
- Instance admin API additions: `SUPERMETRY_ADMIN_API_KEY` (32+ characters) as a bearer token for `/api/private/admin/*`; admin delete of an organization (restorable for 7 days); admin change of a site's `retention_days`; admin creation of users with an optional organization and reset link; and time-limited, audited "sign in as a user" support sessions.
- Pixel analytics projects ("Views anywhere"): 1×1 GIF pixels and SVG view-count badges for GitHub profiles, READMEs and docs. Views are daily aggregates split by image proxy and direct; no IPs or user agents are stored. Up to 100 pixels per project.
- Badges carry a small Supermetry mark; set `SUPERMETRY_BADGE_BRANDING=false` to remove it. `?style=flat|flat-square` and `?color=<hex>` customise them.
- Open counters (`/c/github.com/{owner}[/{repo}].svg`), off by default: enable with `SUPERMETRY_OPEN_COUNTERS=true`; optional `SUPERMETRY_OPEN_COUNTER_BLOCKLIST` file, one key per line.

### Security and upgrade notes

- `POST /api/private/sites`: `retention_days` is now optional (the instance default applies) and so is `revenue_currency` for web sites (the project then stays unconfigured until an owner or admin chooses a currency once in the site settings). Clients that relied on an omitted `retention_days` meaning "keep forever" must send `0` explicitly.

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
