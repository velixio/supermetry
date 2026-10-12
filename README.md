<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="brand/aurora/svg/supermetry-logo-on-dark.svg">
    <img src="brand/aurora/svg/supermetry-logo-on-light.svg" alt="Supermetry" width="280">
  </picture>
</p>

<h1 align="center">Supermetry: the self-hosted alternative to Google Analytics, Plausible and Umami</h1>

<p align="center">
  Open-source, cookie-free web and product analytics. Bring your history with you.<br>
  One Go binary and PostgreSQL. No ClickHouse, no Kafka, no Node in production.
</p>

<p align="center">
  <a href="LICENSE"><img alt="License: Apache-2.0" src="https://img.shields.io/badge/license-Apache--2.0-blue"></a>
  <img alt="Status: pre-release" src="https://img.shields.io/badge/status-pre--release-orange">
  <img alt="Built with Go and PostgreSQL" src="https://img.shields.io/badge/built%20with-Go%20%2B%20PostgreSQL-00ADD8">
</p>

<p align="center">
  Switching from
  <a href="#coming-from-google-analytics">Google Analytics</a> ·
  <a href="#coming-from-plausible">Plausible</a> ·
  <a href="#coming-from-umami">Umami</a> ·
  <a href="#coming-from-mixpanel-or-amplitude">Mixpanel or Amplitude</a>
  <br>
  <a href="#quick-start"><b>Quick start</b></a> ·
  <a href="#at-a-glance">Comparison</a> ·
  <a href="#faq">FAQ</a>
</p>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/dashboard-dark.png">
    <img src="docs/assets/dashboard-light.png" alt="Supermetry web analytics dashboard showing visitors, pageviews, bounce rate, a traffic chart, top pages and traffic sources for a sample website" width="900">
  </picture>
</p>

Supermetry is an open-source, self-hosted analytics platform. It covers web analytics (visitors, pageviews, sources, campaigns) and product analytics (goals, funnels, revenue, retention, journeys, heatmaps and session replay), sets no cookies, and keeps every number in your own PostgreSQL database. Your old data can come with you: Supermetry imports from Google Analytics 4, Plausible, Umami, Mixpanel and Amplitude.

> **Status: pre-release.** Expect breaking changes until `v0.1.0`.

## Coming from Google Analytics

- **Your data stays on your server**, in PostgreSQL, instead of going to Google.
- **No cookies**, no fingerprinting and no raw IP addresses stored, so you need as little consent as possible. Check with your own counsel what that means for your site.
- **A dashboard you can read at a glance**, with the same core reports: sources, campaigns, pages, countries, devices, goals, funnels and revenue.
- **Keep your history.** Export a GA4 report as CSV (or use the Data API or BigQuery) and import it in the project settings. Supermetry imports daily totals with breakdowns by page, landing page, source, country, region, city, device, browser, operating system and event.

## Coming from Plausible

- **No ClickHouse.** Self-hosted Plausible needs ClickHouse next to PostgreSQL. Supermetry needs only PostgreSQL.
- **The features Community Edition leaves out:** funnels, user journeys and revenue goals are in Supermetry's open-source release, along with heatmaps, session replay and Web Vitals.
- **The same numbers.** On identical real-browser traffic, Supermetry and Plausible Community Edition matched on visitors, visits, pageviews, bounce rate, pages, sources, campaigns, devices, browsers, operating systems and custom events ([method and results](tools/bench/accuracy/ab/README.md)).
- **Keep your history** by importing your Plausible data in the project settings.

## Coming from Umami

- **One Go binary instead of a Node.js app.** In a 4-hour soak test at 200 events per second, Supermetry used under 190 MiB of memory with no lost events ([details](tools/bench/soak/README.md)).
- **Visitor IDs reset every day.** The salt changes daily and is deleted after about 48 hours. Umami's default salt rotates monthly.
- **Retention built in.** Old data is removed by dropping whole time partitions, not by bulk deletes.
- **Keep your history** by importing your Umami data in the project settings.

## Coming from Mixpanel or Amplitude

- **Product analytics on your own database:** custom events and properties, funnels, retention cohorts, user journeys, user profiles with `identify`, and revenue.
- **Plus web analytics** in the same place: sources, campaigns, pages, countries and devices.
- **Keep your history.** Import a raw event export (JSON lines, CSV, gzip, or a ZIP of them). IP addresses, user agents and user IDs in the file are never stored.

Every import can be previewed before it runs and undone afterwards, and overlapping days are never counted twice.

## Quick start

You need PostgreSQL (CI runs 18). Redis is not needed. On Linux and macOS:

```sh
curl -fsSL https://github.com/velixio/supermetry/releases/latest/download/install.sh | sh
```

On Windows PowerShell:

```powershell
irm https://github.com/velixio/supermetry/releases/latest/download/install.ps1 | iex
```

Or with Docker Compose and a bundled PostgreSQL:

```sh
git clone https://github.com/velixio/supermetry.git && cd supermetry
cp .env.example .env    # set SUPERMETRY_SECRET_KEY and POSTGRES_PASSWORD
docker compose -f deploy/docker-compose.yml --profile bundled-db up -d --build
```

Open `<your URL>/app` and create your admin account, then add the snippet from **Settings, Installation** to your site:

```html
<script defer src="https://analytics.example.com/js/YOUR_SITE_ID.js"></script>
```

To explore before installing anything on your site, choose **Explore with demo data** after signing up. [deploy/README.md](deploy/README.md) covers unattended installs, upgrades, backups and reverse proxies.

## At a glance

| | Supermetry | Plausible CE (self-hosted) | Umami | Google Analytics |
|---|---|---|---|---|
| Self-hosted | Yes | Yes | Yes | No |
| License | Apache-2.0 | AGPL-3.0 | MIT | Proprietary |
| What you run | One Go binary + PostgreSQL | Elixir app + PostgreSQL + ClickHouse | Node.js app + PostgreSQL | Nothing; Google hosts it |
| Cookies | None | None | None | Yes |
| Funnels, journeys and revenue | Yes | No (Plausible Cloud only) | Yes | Yes |
| Session replay and heatmaps | Yes | No | Yes | No |

<sub>Checked against the Plausible Community Edition and Umami 3.4 source code in October 2026. Corrections are welcome in an issue.</sub>

## All features

- **Web analytics:** visitors, visits, pageviews, bounce rate, visit duration and time on page; realtime; comparison with the previous period or year; breakdowns by page, entry and exit page, source, channel, UTM, country, region, city, language, browser, operating system and device; filters on any dimension and saved segments.
- **Product analytics:** custom events and properties, goals, funnels, revenue, retention cohorts, user journeys and chart annotations; Web Vitals, click and scroll heatmaps and session replay, each opt-in per project; sessions and events explorers and user profiles.
- **Teams:** organizations and projects, roles, two-factor sign-in, shareable links and API keys; alerts by email, webhook, Slack, Discord and Telegram; scheduled email reports.
- **Data in and out:** a REST API, an MCP endpoint, CSV export and the five importers above.
- **Operations:** backup and restore commands, a protected metrics endpoint, a data health page, and country lookups from a bundled [DB-IP](https://db-ip.com) database with optional [MaxMind GeoLite](https://www.maxmind.com) detail.
- **Privacy modes:** `identified` (the default) keeps page paths, referrers and campaigns; `anonymous` is the strictest. [Tracking and privacy](docs/tracking-privacy.md) lists exactly what each records.

## FAQ

**Is Supermetry a free Google Analytics alternative?**
Yes. It is open source under Apache-2.0 and you host it yourself, so there is no per-pageview price.

**Is Supermetry GDPR compliant? Do I need a cookie banner?**
That depends on the laws that apply to you and on what else your site runs, so ask your own counsel. Supermetry is designed to need as little consent as possible: no cookies, no raw IPs and no cross-site tracking, and it honours Do Not Track and Global Privacy Control.

**Can I run Supermetry next to my current tool first?**
Yes. Add both scripts, compare the numbers for a while, then import your history and remove the old script. Live data always wins over imported data for the same day.

**How big a server do I need?**
A small one. In the 4-hour test above, a single process used under 190 MiB of memory at a sustained 200 events per second.

## Documentation

[Deployment](deploy/README.md) · [Configuration](docs/guide/configuration.md) · [Tracking and privacy](docs/tracking-privacy.md) · [API](docs/api.md) · [API authentication](docs/api-auth.md) · [Authorization](docs/authorization.md) · [Notifications](docs/guide/notifications.md)

## Contributing

Build from source with Go and [Bun](https://bun.sh) using `make build`; `make dev` runs a development server with PostgreSQL in Docker. See [CONTRIBUTING.md](CONTRIBUTING.md), and report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## License

[Apache-2.0](LICENSE). Third-party notices are in [NOTICE](NOTICE) and [THIRD_PARTY.md](THIRD_PARTY.md).
