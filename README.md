<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="brand/aurora/svg/supermetry-logo-on-dark.svg">
    <img src="brand/aurora/svg/supermetry-logo-on-light.svg" alt="Supermetry" width="300">
  </picture>
</p>

<h1 align="center">Open-source web analytics you host yourself</h1>

<p align="center">
  <b>Supermetry</b> is a privacy-friendly Google Analytics alternative with funnels, heatmaps and session replay built in.<br>
  No cookies. No ClickHouse. Just one Go binary and PostgreSQL.
</p>

<p align="center">
  <a href="#install-in-one-command"><b>Get started</b></a> ·
  <a href="#how-it-compares">Compare</a> ·
  <a href="#proof-not-promises">Benchmarks</a> ·
  <a href="#documentation">Docs</a> ·
  <a href="#faq">FAQ</a>
</p>

<p align="center">
  <a href="LICENSE"><img alt="License: Apache-2.0" src="https://img.shields.io/badge/license-Apache--2.0-blue"></a>
  <img alt="Status: pre-release" src="https://img.shields.io/badge/status-pre--release-orange">
  <img alt="Tracker size: about 1.2 KB gzipped" src="https://img.shields.io/badge/tracker-~1.2%20KB%20gzip-success">
  <img alt="No cookies" src="https://img.shields.io/badge/cookies-none-success">
</p>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/dashboard-dark.png">
    <img src="docs/assets/dashboard-light.png" alt="Supermetry web analytics dashboard showing visitors, pageviews, bounce rate, a traffic chart, top pages and traffic sources for a sample website" width="900">
  </picture>
</p>

> **Status: pre-release.** Expect breaking changes until `v0.1.0`.

## Everything in one binary

<table>
  <tr>
    <td width="33%" valign="top">
      <h3>Web analytics</h3>
      Visitors, pageviews, bounce rate, sources, UTM campaigns, countries and devices. Realtime, filters on any dimension, saved segments and period comparison.
    </td>
    <td width="33%" valign="top">
      <h3>Product analytics</h3>
      Goals, funnels, revenue, retention cohorts and user journeys. Web Vitals, click and scroll heatmaps, and session replay, each opt-in per project.
    </td>
    <td width="33%" valign="top">
      <h3>Privacy by design</h3>
      No cookies, no fingerprinting, no raw IPs stored. A random daily salt that is deleted after about 48 hours. Honours Do Not Track and Global Privacy Control.
    </td>
  </tr>
  <tr>
    <td valign="top">
      <h3>Simple to run</h3>
      One Go binary and PostgreSQL. No Redis, Kafka or Node in production. Installers for Linux, macOS and Windows, plus backup and restore.
    </td>
    <td valign="top">
      <h3>Built for teams</h3>
      Organizations, roles, two-factor sign-in and shareable links. Alerts by email, webhook, Slack, Discord and Telegram, and scheduled email reports.
    </td>
    <td valign="top">
      <h3>Your data, portable</h3>
      Apache-2.0. A REST API, an MCP endpoint, CSV export, and imports from Google Analytics 4, Plausible, Umami, Mixpanel and Amplitude, so you can move in or out without losing your data.
    </td>
  </tr>
</table>

## Install in one command

On a server with PostgreSQL (CI runs 18):

```sh
curl -fsSL https://github.com/velixio/supermetry/releases/latest/download/install.sh | sh
```

<details>
<summary>Windows, Docker Compose and dry run</summary>

Windows PowerShell:

```powershell
irm https://github.com/velixio/supermetry/releases/latest/download/install.ps1 | iex
```

Docker Compose, with a bundled PostgreSQL:

```sh
git clone https://github.com/velixio/supermetry.git && cd supermetry
cp .env.example .env    # set SUPERMETRY_SECRET_KEY and POSTGRES_PASSWORD
docker compose -f deploy/docker-compose.yml --profile bundled-db up -d --build
```

To see what the installer would do first: `curl -fsSL https://github.com/velixio/supermetry/releases/latest/download/install.sh | sh -s -- --dry-run`.

</details>

Open `<your URL>/app`, create your admin account, and paste the snippet from **Settings, Installation** into your site:

```html
<script defer src="https://analytics.example.com/js/YOUR_SITE_ID.js"></script>
```

Want to look around first? Choose **Explore with demo data** after signing up for a project with 90 days of live sample traffic. It deletes itself after 24 hours.

Full guides: [deployment](deploy/README.md) (unattended installs, upgrades, backups, reverse proxies) and [configuration](docs/guide/configuration.md).

## How it compares

| | Supermetry | Plausible CE (self-hosted) | Umami | Google Analytics |
|---|---|---|---|---|
| Self-hosted | ✅ | ✅ | ✅ | ❌ |
| License | Apache-2.0 | AGPL-3.0 | MIT | Proprietary |
| What you run | One Go binary + PostgreSQL | Elixir app + PostgreSQL + ClickHouse | Node.js app + PostgreSQL | Nothing; Google hosts it |
| Cookie-free | ✅ | ✅ | ✅ | ❌ |
| Funnels, journeys and revenue | ✅ | ❌ (Plausible Cloud only) | ✅ | ✅ |
| Session replay and heatmaps | ✅ | ❌ | ✅ | ❌ |

<sub>Checked against the Plausible Community Edition and Umami 3.4 source code in October 2026. Corrections are welcome in an issue.</sub>

## Proof, not promises

- **Light.** In a 4-hour soak test the server ingested 200 events per second, 2.88 million events in all, with no loss, no errors and a worst-case p99 latency of 23 ms, using under 190 MiB of memory. [Method and results](tools/bench/soak/README.md).
- **Accurate.** Supermetry and Plausible Community Edition received identical traffic from real browsers. Visitors, visits, pageviews, bounce rate, pages, sources, campaigns, devices, browsers, operating systems and custom events all matched, in both products and against hand-computed truth. [Method and results](tools/bench/accuracy/ab/README.md).
- **Easy on Postgres.** Raw data lives in time partitions that are dropped for retention instead of bulk-deleted.

## Privacy you can configure

Supermetry never uses cookies or stores raw IP addresses. By default it keeps page paths, referrers and campaigns. Set `SUPERMETRY_TRACKING_MODE=anonymous` for the strictest mode, which also hides page paths unless you list them as public, and drops referrers, campaigns, event properties and user profiles. Session replay and heatmaps are separate opt-ins. [Tracking and privacy](docs/tracking-privacy.md) lists exactly what each setting records.

## FAQ

<details>
<summary><b>Is Supermetry a Google Analytics alternative?</b></summary>

Yes. It measures visitors, pageviews, sources, goals and conversions without cookies, and the data stays in your own PostgreSQL database.

</details>

<details>
<summary><b>Is Supermetry GDPR compliant? Do I need a cookie banner?</b></summary>

That depends on the laws that apply to you and on what else your site runs, so ask your own counsel. Supermetry is designed to need as little consent as possible: no cookies, no raw IPs and no cross-site tracking, and it honours Do Not Track and Global Privacy Control.

</details>

<details>
<summary><b>Does it store IP addresses?</b></summary>

No. IP addresses are used to look up the country (and city, if you add MaxMind data) and to derive a daily-salted visitor ID, and are then discarded. See [Tracking and privacy](docs/tracking-privacy.md).

</details>

<details>
<summary><b>How is it different from Plausible or Umami?</b></summary>

Self-hosted Plausible needs ClickHouse next to PostgreSQL and leaves out funnels, journeys and revenue goals. Umami runs on Node.js. Supermetry is a single Go binary on PostgreSQL alone, with every feature in the open-source release. See [how it compares](#how-it-compares).

</details>

<details>
<summary><b>Can I import my data from Google Analytics, Plausible or Umami?</b></summary>

Yes. In the project settings, import a Google Analytics 4 report export or data from Plausible, Umami, Mixpanel or Amplitude. Overlapping days are never counted twice, and you can export to CSV whenever you like.

</details>

<details>
<summary><b>How big a server do I need?</b></summary>

A small one. In the 4-hour test above, a single process used under 190 MiB of memory at a sustained 200 events per second.

</details>

## Documentation

[Deployment](deploy/README.md) · [Configuration](docs/guide/configuration.md) · [Tracking and privacy](docs/tracking-privacy.md) · [API](docs/api.md) · [API authentication](docs/api-auth.md) · [Authorization](docs/authorization.md) · [Notifications](docs/guide/notifications.md)

## Contributing

Build from source with Go and [Bun](https://bun.sh) using `make build`; `make dev` runs a development server with PostgreSQL in Docker. [CONTRIBUTING.md](CONTRIBUTING.md) has the details. Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## License

[Apache-2.0](LICENSE). Third-party notices are in [NOTICE](NOTICE) and [THIRD_PARTY.md](THIRD_PARTY.md). Country lookups use a bundled [DB-IP](https://db-ip.com) database, with optional [MaxMind GeoLite](https://www.maxmind.com) data.
