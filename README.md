<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="brand/aurora/svg/supermetry-logo-on-dark.svg">
    <img src="brand/aurora/svg/supermetry-logo-on-light.svg" alt="Supermetry" width="280">
  </picture>
</p>

<h1 align="center">Supermetry: open-source, self-hosted web analytics</h1>

<p align="center">
  A privacy-friendly, cookie-free Google Analytics alternative that you run yourself.<br>
  One Go binary and PostgreSQL. No ClickHouse, no Kafka, no Node in production.
</p>

<p align="center">
  <a href="LICENSE"><img alt="License: Apache-2.0" src="https://img.shields.io/badge/license-Apache--2.0-blue"></a>
  <img alt="Status: pre-release" src="https://img.shields.io/badge/status-pre--release-orange">
  <img alt="Built with Go and PostgreSQL" src="https://img.shields.io/badge/built%20with-Go%20%2B%20PostgreSQL-00ADD8">
</p>

<p align="center">
  <a href="#quick-start"><b>Quick start</b></a> ·
  <a href="#features">Features</a> ·
  <a href="#supermetry-vs-plausible-umami-and-google-analytics">Compare</a> ·
  <a href="#documentation">Docs</a> ·
  <a href="#faq">FAQ</a>
</p>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/dashboard-dark.png">
    <img src="docs/assets/dashboard-light.png" alt="Supermetry web analytics dashboard showing visitors, pageviews, bounce rate, a traffic chart, top pages and traffic sources for a sample website" width="900">
  </picture>
  <br>
  <sub>The Supermetry dashboard, showing the built-in sample project.</sub>
</p>

Supermetry is an open-source, self-hosted web analytics platform and a privacy-friendly alternative to Google Analytics. It counts visitors, pageviews, traffic sources, goals and conversions without cookies, and keeps every number in your own PostgreSQL database. Funnels, retention, user journeys, Web Vitals, heatmaps and session replay are included in the open-source release.

> **Status: pre-release.** Expect breaking changes until `v0.1.0`.

## Why Supermetry

- **Simple to run.** One Go binary and a PostgreSQL database. Redis is not needed. Installers for Linux, macOS and Windows, plus backup and restore commands.
- **Privacy by design.** No cookies, no fingerprinting, and no raw IP addresses stored. Visitors are counted with a random daily salt that is deleted after about 48 hours. The tracker honours Do Not Track and Global Privacy Control.
- **Light on your servers and your pages.** The tracking script is about 1.2 KB gzipped. In a 4-hour test the server ingested 200 events per second, 2.88 million events in all, with no loss, no errors and a worst-case p99 latency of 23 ms, using under 190 MiB of memory ([details](tools/bench/soak/README.md)).
- **Numbers you can check.** In a controlled side-by-side run, Supermetry and Plausible Community Edition received identical traffic from real browsers. Visitors, visits, pageviews, bounce rate, pages, sources, campaigns, devices, browsers, operating systems and custom events all matched, in both products and against hand-computed truth ([details](tools/bench/accuracy/ab/README.md)).
- **Open.** Apache-2.0 licensed, with a REST API, an MCP endpoint, CSV export and imports from Google Analytics 4, Plausible, Umami, Mixpanel and Amplitude, so you can move in or out without losing your data.

## Quick start

You need a server with PostgreSQL (CI runs 18). Redis is not needed.

**Installer.** On Linux and macOS:

```sh
curl -fsSL https://github.com/velixio/supermetry/releases/latest/download/install.sh | sh
```

On Windows, in PowerShell:

```powershell
irm https://github.com/velixio/supermetry/releases/latest/download/install.ps1 | iex
```

Add `--dry-run` to see what the installer would do first, for example `curl -fsSL https://github.com/velixio/supermetry/releases/latest/download/install.sh | sh -s -- --dry-run`.

**Docker Compose**, with a bundled PostgreSQL:

```sh
git clone https://github.com/velixio/supermetry.git && cd supermetry
cp .env.example .env    # set SUPERMETRY_SECRET_KEY and POSTGRES_PASSWORD
docker compose -f deploy/docker-compose.yml --profile bundled-db up -d --build
```

Then open `<your URL>/app` (`http://localhost:8787/app` for Docker) and create your admin account. [deploy/README.md](deploy/README.md) covers unattended installs, an external database, service management, upgrades, backups and reverse proxies, and [docs/guide/configuration.md](docs/guide/configuration.md) lists every setting.

**Add the tracking script** to your site. Copy the exact snippet from **Settings, Installation** in the dashboard. It looks like this:

```html
<script defer src="https://analytics.example.com/js/YOUR_SITE_ID.js"></script>
```

**Not ready to install the tracker?** Choose **Explore with demo data** after you sign up. Supermetry creates a project with 90 days of realistic sample traffic and keeps it moving while you look around. It deletes itself after 24 hours, and you can remove it earlier.

## Features

**Web analytics**
- Visitors, visits, pageviews, bounce rate, visit duration and time on page, with a realtime view and comparison with the previous period or year.
- Breakdowns by page, entry and exit page, source, channel, UTM parameters, country, region, city, language, browser, operating system and device. Filters on any dimension, and saved segments.
- Custom events and properties, goals, funnels, revenue, retention cohorts, user journeys and annotations on the chart.

**Product analytics**
- Web Vitals (LCP, CLS, INP, FCP, TTFB), heatmaps with click and scroll maps, and session replay, each opt-in per project.
- A sessions explorer, an events explorer and user profiles with `identify` (opt-in per project).

**Teams and integrations**
- Organizations and projects, roles, two-factor sign-in, shareable links and API keys.
- Alerts with email, webhook, Slack, Discord and Telegram delivery, and scheduled email reports.
- A REST API and an MCP endpoint, CSV export, and imports from Google Analytics 4, Plausible, Umami, Mixpanel and Amplitude.

**Operations**
- PostgreSQL only, with time partitions that are dropped for retention instead of bulk deletes.
- Backup and restore commands, a protected metrics endpoint and a data health page.
- Country lookups from a bundled [DB-IP](https://db-ip.com) database, with optional region and city detail from [MaxMind GeoLite](https://www.maxmind.com).

## Supermetry vs Plausible, Umami and Google Analytics

| | Supermetry | Plausible CE (self-hosted) | Umami | Google Analytics |
|---|---|---|---|---|
| Self-hosted | Yes | Yes | Yes | No |
| License | Apache-2.0 | AGPL-3.0 | MIT | Proprietary |
| What you run | One Go binary + PostgreSQL | Elixir app + PostgreSQL + ClickHouse | Node.js app + PostgreSQL | Nothing; Google hosts it |
| Cookies | None | None | None | Yes |
| Funnels, journeys and revenue | Yes | No (Plausible Cloud only) | Yes | Yes |
| Session replay and heatmaps | Yes | No | Yes | No |

<sub>Checked against the Plausible Community Edition and Umami 3.4 source code in October 2026. Corrections are welcome in an issue.</sub>

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

## FAQ

**Is Supermetry a Google Analytics alternative?**
Yes. It measures visitors, pageviews, sources, goals and conversions without cookies, and the data stays in your own PostgreSQL database.

**Does it use cookies or store IP addresses?**
It sets no cookies. IP addresses are used to look up the country (and city, if you add MaxMind data) and to derive a daily-salted visitor ID, and are not stored. See [Tracking and privacy](docs/tracking-privacy.md).

**Is Supermetry GDPR compliant? Do I need a cookie banner?**
That depends on the laws that apply to you and on what else your site runs, so ask your own counsel. Supermetry is designed to need as little consent as possible: no cookies, no raw IPs and no cross-site tracking, and it honours Do Not Track and Global Privacy Control.

**How is it different from Plausible or Umami?**
Self-hosted Plausible needs ClickHouse next to PostgreSQL and leaves out funnels, journeys and revenue goals. Umami runs on Node.js. Supermetry is a single Go binary on PostgreSQL alone, with every feature in the open-source release. See the [comparison table](#supermetry-vs-plausible-umami-and-google-analytics).

**Can I import my data from Google Analytics, Plausible or Umami?**
Yes. In the project settings, import a Google Analytics 4 report export or data from Plausible, Umami, Mixpanel or Amplitude. Overlapping days are never counted twice, and you can export to CSV whenever you like.

**What does it need to run?**
A small server and PostgreSQL. In the 4-hour test above, a single process used under 190 MiB of memory at a sustained 200 events per second.

## Documentation

- [Deployment](deploy/README.md) and [configuration](docs/guide/configuration.md)
- [Tracking and privacy](docs/tracking-privacy.md)
- [API](docs/api.md) and [API authentication](docs/api-auth.md)
- [Authorization](docs/authorization.md)
- [Notifications](docs/guide/notifications.md)

## Build from source and development

<details>
<summary>Build from source</summary>

You need Go and [Bun](https://bun.sh).

```sh
make build                       # builds the dashboard, then the binary in bin/
export SUPERMETRY_DATABASE_URL=postgres://user:pass@127.0.0.1:5432/supermetry
export SUPERMETRY_SECRET_KEY=$(openssl rand -base64 32)
export SUPERMETRY_BASE_URL=http://localhost:8787
./bin/supermetry
```

Migrations run at startup. `bin/supermetry --help` lists the other commands (`backup`, `restore`, `migrate`, `close-days`, `geoip`).

</details>

<details>
<summary>Development</summary>

You need Go, [Bun](https://bun.sh) and Docker. No database settings are needed: unless `.env` sets
`SUPERMETRY_DATABASE_URL` (or `SUPERMETRY_TEST_DSN` for tests), `make` starts PostgreSQL in Docker on
port 5433 and uses it. `make dev-db-down` stops it; its data is kept.

```sh
make dev     # build and run the server
make test    # Go tests
make lint    # go vet and gofmt
```

Dashboard code is in `web/` and uses Bun (`bun install --frozen-lockfile`, `bun run test`).

</details>

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md). Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## License

[Apache-2.0](LICENSE). Third-party notices are in [NOTICE](NOTICE) and [THIRD_PARTY.md](THIRD_PARTY.md).
