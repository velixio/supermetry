<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="brand/aurora/svg/supermetry-logo-on-dark.svg">
    <img src="brand/aurora/svg/supermetry-logo-on-light.svg" alt="Supermetry" width="240">
  </picture>
</p>

# Supermetry

**Open-source, self-hosted web analytics.** A cookie-free Google Analytics alternative that runs as one Go binary on PostgreSQL, with funnels, retention, journeys, heatmaps and session replay included.

[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
![Status: pre-release](https://img.shields.io/badge/status-pre--release-orange)
![Built with Go and PostgreSQL](https://img.shields.io/badge/built%20with-Go%20%2B%20PostgreSQL-00ADD8)

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/dashboard-dark.png">
  <img src="docs/assets/dashboard-light.png" alt="Supermetry web analytics dashboard showing visitors, pageviews, bounce rate, a traffic chart, top pages and traffic sources for a sample website" width="900">
</picture>

> **Pre-release.** Expect breaking changes until `v0.1.0`.

## Install

You need PostgreSQL. Redis is not needed.

```sh
curl -fsSL https://github.com/velixio/supermetry/releases/latest/download/install.sh | sh
```

Windows: `irm https://github.com/velixio/supermetry/releases/latest/download/install.ps1 | iex`. Docker Compose, unattended installs, upgrades, backups and reverse proxies: [deploy/README.md](deploy/README.md).

Open `<your URL>/app`, create your admin account, and add the snippet from **Settings, Installation** to your site. To look around first, choose **Explore with demo data**.

## What you get

- **Web analytics:** visitors, pageviews, bounce rate, sources, UTM campaigns, countries and devices, with realtime, filters and saved segments.
- **Product analytics:** goals, funnels, revenue, retention, user journeys, Web Vitals, heatmaps and session replay.
- **Privacy:** no cookies, no raw IPs stored, a daily salt deleted after about 48 hours, and an [anonymous mode](docs/tracking-privacy.md).
- **Light:** a ~1.2 KB tracker; 200 events/s for 4 hours with no loss and under 190 MiB of memory ([soak test](tools/bench/soak/README.md)).
- **Accurate:** matched Plausible on identical real-browser traffic ([A/B test](tools/bench/accuracy/ab/README.md)).
- **Portable:** REST API, MCP endpoint, CSV export, and imports from Google Analytics 4, Plausible, Umami, Mixpanel and Amplitude.

## Why not Plausible or Umami?

Both are good. Self-hosted Plausible needs ClickHouse next to PostgreSQL and leaves out funnels, journeys and revenue goals. Umami runs on Node.js. Supermetry gives you the full feature set from a single Go binary and PostgreSQL alone.

## Docs

[Configuration](docs/guide/configuration.md) · [Tracking and privacy](docs/tracking-privacy.md) · [API](docs/api.md) · [API auth](docs/api-auth.md) · [Authorization](docs/authorization.md) · [Notifications](docs/guide/notifications.md)

## Develop

```sh
make dev     # needs Go, Bun and Docker; starts PostgreSQL for you
make test
```

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md).

## License

[Apache-2.0](LICENSE). Notices: [NOTICE](NOTICE), [THIRD_PARTY.md](THIRD_PARTY.md).
