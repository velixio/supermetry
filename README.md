<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/brand/aurora/svg/supermetry-logo-on-dark.svg">
    <img src="docs/brand/aurora/svg/supermetry-logo-on-light.svg" alt="Supermetry" width="280">
  </picture>
</p>

<h1 align="center">Supermetry: self-hosted, privacy-friendly web analytics</h1>

<p align="center">
  An open-source, cookie-free alternative to Google Analytics that you run yourself.<br>
  One Go binary and PostgreSQL. No ClickHouse, no Kafka, no Node in production.
</p>

<p align="center">
  <a href="LICENSE"><img alt="License: Apache-2.0" src="https://img.shields.io/badge/license-Apache--2.0-blue"></a>
  <img alt="Status: pre-release" src="https://img.shields.io/badge/status-pre--release-orange">
  <img alt="Built with Go and PostgreSQL" src="https://img.shields.io/badge/built%20with-Go%20%2B%20PostgreSQL-00ADD8">
</p>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/dashboard-dark.png">
    <img src="docs/assets/dashboard-light.png" alt="Supermetry web analytics dashboard showing visitors, pageviews, bounce rate, a traffic chart, top pages and traffic sources for a sample website" width="900">
  </picture>
  <br>
  <sub>The Supermetry dashboard, showing the built-in sample project.</sub>
</p>

> **Status: pre-release.** Expect breaking changes until `v0.1.0`.

## Why Supermetry

- **Simple to run.** One Go binary and a PostgreSQL database. Redis is not needed. Installers for Linux, macOS and Windows, plus backup and restore commands.
- **Privacy by design.** No cookies, no fingerprinting, and no raw IP addresses stored. Visitors are counted with a random daily salt that is deleted after about 48 hours. The tracker honours Do Not Track and Global Privacy Control.
- **Light on your servers and your pages.** The tracking script is about 1.2 KB gzipped. In a 4-hour test the server ingested 200 events per second, 2.88 million events in all, with no loss, no errors and a worst-case p99 latency of 23 ms, using under 190 MiB of memory ([details](tools/bench/soak/README.md)).
- **Numbers you can check.** In a controlled side-by-side run, Supermetry and Plausible Community Edition received identical traffic from real browsers. Visitors, visits, pageviews, bounce rate, pages, sources, campaigns, devices, browsers, operating systems and custom events all matched, in both products and against hand-computed truth ([details](tools/bench/accuracy/ab/README.md)).
- **More than pageviews.** Goals, funnels, revenue, retention, user journeys, Web Vitals, heatmaps and session replay, all in the open-source release.
- **Open.** Apache-2.0 licensed, with a REST API, an MCP endpoint and imports from Plausible and Umami, so you can leave without losing your data.

## Features

**Analytics**
- Visitors, visits, pageviews, bounce rate, visit duration and time on page, with a realtime view and comparison with the previous period or year.
- Breakdowns by page, entry and exit page, source, channel, UTM parameters, country, region, city, language, browser, operating system and device. Filters on any dimension, and saved segments.
- Custom events and properties, goals, funnels, revenue, retention cohorts, user journeys and annotations on the chart.

**Product analytics**
- Web Vitals (LCP, CLS, INP, FCP, TTFB), heatmaps with click and scroll maps, and session replay, each opt-in per project.
- A sessions explorer, an events explorer and user profiles with `identify` (opt-in per project).

**Teams and integrations**
- Organizations and projects, roles, two-factor sign-in, shareable links and API keys.
- Alerts with email, webhook, Slack, Discord and Telegram delivery, and scheduled email reports.
- A REST API and an MCP endpoint, CSV export, and imports from Plausible and Umami.

**Operations**
- PostgreSQL only, with time partitions that are dropped for retention instead of bulk deletes.
- Backup and restore commands, a protected metrics endpoint and a data health page.
- Country lookups from a bundled [DB-IP](https://db-ip.com) database, with optional region and city detail from [MaxMind GeoLite](https://www.maxmind.com).

## Try it with sample data

After you sign up, choose **Explore with demo data**. Supermetry creates a project with 90 days of realistic sample traffic and keeps it moving while you look around, so you can explore every report before you install the tracker. It deletes itself after 24 hours, and you can remove it earlier.

## Privacy modes

Supermetry collects page paths, referrers and campaigns by default, and never uses cookies or stores raw IP addresses. For the strictest setup, switch the whole server to anonymous mode.

| | `anonymous` | `identified` (default) |
|---|---|---|
| Visitors, visits, pageviews and charts over time | yes | yes |
| Country, device, browser, operating system and channel | yes | yes |
| Page paths | replaced by a placeholder unless you list public paths | yes (query strings are always dropped) |
| Sessions and events explorers, imports | no | yes |
| Referrers, UTM campaigns, event properties, user profiles | no | yes, in projects with profiles on (the default for new projects) |

Set `SUPERMETRY_TRACKING_MODE=anonymous` on the server to choose the strictest mode, and turn **profiles** off in a project's settings to stop keeping referrers, campaigns and event properties for that project. Session replay and heatmaps are separate opt-ins. See [Tracking and privacy](docs/tracking-privacy.md) for exactly what each setting records.

## Requirements

- PostgreSQL (18 is what CI runs).
- Redis is not needed. A single instance runs without it.

## Quick start

Linux and macOS:

```sh
curl -fsSL https://github.com/velixio/supermetry/releases/latest/download/install.sh | sh
```

Windows PowerShell:

```powershell
irm https://github.com/velixio/supermetry/releases/latest/download/install.ps1 | iex
```

To see what the installer would do first, add `--dry-run`, for example `curl -fsSL https://github.com/velixio/supermetry/releases/latest/download/install.sh | sh -s -- --dry-run`.

Open `<your URL>/app` and create your admin account. See [deploy/README.md](deploy/README.md) for unattended installs, manual deployment, service management, upgrades, backups and reverse proxies, and [docs/guide/configuration.md](docs/guide/configuration.md) for every setting.

Then add the tracking script to your site. Copy the exact snippet from **Settings, Installation** in the dashboard. It looks like this:

```html
<script defer src="https://analytics.example.com/js/YOUR_SITE_ID.js"></script>
```

## FAQ

**Is Supermetry a Google Analytics alternative?**
Yes. It measures visitors, pageviews, sources, goals and conversions without cookies, and the data stays in your own PostgreSQL database.

**Does it use cookies or store IP addresses?**
It sets no cookies. IP addresses are used to look up the country (and city, if you add MaxMind data) and to derive a daily-salted visitor ID, and are not stored. See [Tracking and privacy](docs/tracking-privacy.md).

**Do I need a cookie banner?**
That depends on the laws that apply to you and on what else your site runs, so ask your own counsel. Supermetry is designed to need as little consent as possible: no cookies, no raw IPs and no cross-site tracking, and it honours Do Not Track and Global Privacy Control.

**How is it different from Plausible or Umami?**
Plausible's self-hosted edition needs ClickHouse next to PostgreSQL, and Umami runs on Node.js. Supermetry is a single Go binary and PostgreSQL only, and it includes funnels, revenue, retention, session replay and heatmaps in the open-source release.

**Can I move my existing data in?**
Yes. Import from Plausible or Umami in the project settings, then export to CSV whenever you like.

**What does it need to run?**
A small server and PostgreSQL. In the 4-hour test above, a single process used under 190 MiB of memory at a sustained 200 events per second.

## Documentation

- [Configuration](docs/guide/configuration.md)
- [Tracking and privacy](docs/tracking-privacy.md)
- [API](docs/api.md) and [API authentication](docs/api-auth.md)
- [Authorization](docs/authorization.md)
- [Notifications](docs/guide/notifications.md)

## Build from source

You need Go and [Bun](https://bun.sh).

```sh
make build                       # builds the dashboard, then the binary in bin/
export SUPERMETRY_DATABASE_URL=postgres://user:pass@127.0.0.1:5432/supermetry
export SUPERMETRY_SECRET_KEY=$(openssl rand -base64 32)
export SUPERMETRY_BASE_URL=http://localhost:8787
./bin/supermetry
```

Migrations run at startup. `bin/supermetry --help` lists the other commands (`backup`, `restore`, `migrate`, `close-days`, `geoip`).

## Development

You need Go, [Bun](https://bun.sh) and Docker. No database settings are needed: unless `.env` sets
`SUPERMETRY_DATABASE_URL` (or `SUPERMETRY_TEST_DSN` for tests), `make` starts PostgreSQL in Docker on
port 5433 and uses it. `make dev-db-down` stops it; its data is kept.

```sh
make dev     # watch mode: open http://localhost:8787 (UI hot reload, Go restarts on change; listens on 127.0.0.1 only)
make run     # embedded build served on :8787
make dev-embedded  # one-shot embedded UI build and run, no watching
make test    # Go tests
make lint    # go vet and gofmt
```

Dashboard code is in `web/` and uses Bun (`bun install --frozen-lockfile`, `bun run test`).

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md). Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## License

[Apache-2.0](LICENSE). Third-party notices are in [NOTICE](NOTICE) and [THIRD_PARTY.md](THIRD_PARTY.md).
