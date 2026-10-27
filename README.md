<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/brand/aurora/svg/supermetry-logo-on-dark.svg">
    <img src="docs/brand/aurora/svg/supermetry-logo-on-light.svg" alt="Supermetry" width="260">
  </picture>
</p>

<h1 align="center">Find the bottleneck. Own every event.</h1>

<p align="center">
  Open-source, privacy-first web and product analytics.<br>
  <b>One Go binary + PostgreSQL. No cookies.</b>
</p>

<p align="center">
  <a href="https://github.com/velixio/supermetry/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/velixio/supermetry?style=flat-square&logo=github&label=release&color=6d5dfc"></a>
  <a href="LICENSE"><img alt="License: Apache-2.0" src="https://img.shields.io/badge/license-Apache--2.0-3b82f6?style=flat-square"></a>
  <img alt="Go and PostgreSQL" src="https://img.shields.io/badge/Go%20%2B%20PostgreSQL-00ADD8?style=flat-square&logo=go&logoColor=white">
  <img alt="Tracker about 1.2 KB gzipped" src="https://img.shields.io/badge/tracker-~1.2%20KB-22c55e?style=flat-square">
  <a href="https://github.com/velixio/supermetry/stargazers"><img alt="GitHub stars" src="https://img.shields.io/github/stars/velixio/supermetry?style=flat-square&logo=github&color=f5b301"></a>
</p>

<p align="center">
  <a href="#quick-start"><img alt="Self-host" src="https://img.shields.io/badge/Self--host-6d5dfc?style=for-the-badge&logo=data%3Aimage%2Fsvg%2Bxml%3Bbase64%2CPHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAyNCAyNCIgd2lkdGg9IjI0IiBoZWlnaHQ9IjI0Ij48ZyBmaWxsPSJub25lIiBzdHJva2U9IiNmZmZmZmYiIHN0cm9rZS1saW5lY2FwPSJyb3VuZCIgc3Ryb2tlLWxpbmVqb2luPSJyb3VuZCIgc3Ryb2tlLXdpZHRoPSIyIj48cmVjdCB3aWR0aD0iMjAiIGhlaWdodD0iOCIgeD0iMiIgeT0iMiIgcng9IjIiIHJ5PSIyIi8%2BPHJlY3Qgd2lkdGg9IjIwIiBoZWlnaHQ9IjgiIHg9IjIiIHk9IjE0IiByeD0iMiIgcnk9IjIiLz48cGF0aCBkPSJNNiA2aC4wMU02IDE4aC4wMSIvPjwvZz48L3N2Zz4%3D&logoColor=white"></a>
  <a href="https://app.supermetry.com/demo?utm_source=github&utm_medium=readme&utm_campaign=supermetry-repo&utm_content=demo-badge"><img alt="Live demo" src="https://img.shields.io/badge/Live%20demo-6d5dfc?style=for-the-badge"></a>
  <a href="https://supermetry.com/docs/?utm_source=github&utm_medium=readme&utm_campaign=supermetry-repo&utm_content=docs-badge"><img alt="Docs" src="https://img.shields.io/badge/Docs-18181b?style=for-the-badge&logo=data%3Aimage%2Fsvg%2Bxml%3Bbase64%2CPHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAyNCAyNCIgd2lkdGg9IjI0IiBoZWlnaHQ9IjI0Ij48cGF0aCBmaWxsPSJub25lIiBzdHJva2U9IiNmZmZmZmYiIHN0cm9rZS1saW5lY2FwPSJyb3VuZCIgc3Ryb2tlLWxpbmVqb2luPSJyb3VuZCIgc3Ryb2tlLXdpZHRoPSIyIiBkPSJNMTIgNXYxNm04LjAwMS0yQTIgMiAwIDAgMCAyMiAxN1Y1YTIgMiAwIDAgMC0xLjk5OS0yTDE2IDMuMDAyQTUgNSAwIDAgMCAxMiA1YTUgNSAwIDAgMC00LTJINGEyIDIgMCAwIDAtMiAydjEyYTIgMiAwIDAgMCAxLjk5OSAySDhhNSA1IDAgMCAxIDQgMmE1IDUgMCAwIDEgNC0yeiIvPjwvc3ZnPg%3D%3D&logoColor=white"></a>
  <a href="https://supermetry.com/?utm_source=github&utm_medium=readme&utm_campaign=supermetry-repo&utm_content=website-badge"><img alt="Website" src="https://img.shields.io/badge/Website-18181b?style=for-the-badge&logo=data%3Aimage%2Fsvg%2Bxml%3Bbase64%2CPHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAyNCAyNCIgd2lkdGg9IjI0IiBoZWlnaHQ9IjI0Ij48ZyBmaWxsPSJub25lIiBzdHJva2U9IiNmZmZmZmYiIHN0cm9rZS1saW5lY2FwPSJyb3VuZCIgc3Ryb2tlLWxpbmVqb2luPSJyb3VuZCIgc3Ryb2tlLXdpZHRoPSIyIj48Y2lyY2xlIGN4PSIxMiIgY3k9IjEyIiByPSIxMCIvPjxwYXRoIGQ9Ik0xMiAyYTE0LjUgMTQuNSAwIDAgMCAwIDIwYTE0LjUgMTQuNSAwIDAgMCAwLTIwTTIgMTJoMjAiLz48L2c%2BPC9zdmc%2B&logoColor=white"></a>
  <a href="#supermetry-vs-plausible-umami-and-google-analytics"><img alt="Compare" src="https://img.shields.io/badge/Compare-18181b?style=for-the-badge&logo=data%3Aimage%2Fsvg%2Bxml%3Bbase64%2CPHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAyNCAyNCIgd2lkdGg9IjI0IiBoZWlnaHQ9IjI0Ij48ZyBmaWxsPSJub25lIiBzdHJva2U9IiNmZmZmZmYiIHN0cm9rZS1saW5lY2FwPSJyb3VuZCIgc3Ryb2tlLWxpbmVqb2luPSJyb3VuZCIgc3Ryb2tlLXdpZHRoPSIyIj48cGF0aCBkPSJNMTIgM3YxOG03LTEzbDMgOGE1IDUgMCAwIDEtNiAwelY3Ii8%2BPHBhdGggZD0iTTMgN2gxYTE3IDE3IDAgMCAwIDgtMmExNyAxNyAwIDAgMCA4IDJoMU01IDhsMyA4YTUgNSAwIDAgMS02IDB6VjdtMiAxNGgxMCIvPjwvZz48L3N2Zz4%3D&logoColor=white"></a>
  <a href="https://app.supermetry.com/signup?utm_source=github&utm_medium=readme&utm_campaign=supermetry-repo&utm_content=cloud-badge"><img alt="EU Cloud" src="https://img.shields.io/badge/EU%20Cloud-18181b?style=for-the-badge&logo=data%3Aimage%2Fsvg%2Bxml%3Bbase64%2CPHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAyNCAyNCIgd2lkdGg9IjI0IiBoZWlnaHQ9IjI0Ij48cGF0aCBmaWxsPSJub25lIiBzdHJva2U9IiNmZmZmZmYiIHN0cm9rZS1saW5lY2FwPSJyb3VuZCIgc3Ryb2tlLWxpbmVqb2luPSJyb3VuZCIgc3Ryb2tlLXdpZHRoPSIyIiBkPSJNMTcuNSAxOUg5YTcgNyAwIDEgMSA2LjcxLTloMS43OWE0LjUgNC41IDAgMSAxIDAgOSIvPjwvc3ZnPg%3D%3D&logoColor=white"></a>
  <a href="https://github.com/velixio/supermetry/releases/latest"><img alt="Download" src="https://img.shields.io/badge/Download-18181b?style=for-the-badge&logo=data%3Aimage%2Fsvg%2Bxml%3Bbase64%2CPHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAyNCAyNCIgd2lkdGg9IjI0IiBoZWlnaHQ9IjI0Ij48ZyBmaWxsPSJub25lIiBzdHJva2U9IiNmZmZmZmYiIHN0cm9rZS1saW5lY2FwPSJyb3VuZCIgc3Ryb2tlLWxpbmVqb2luPSJyb3VuZCIgc3Ryb2tlLXdpZHRoPSIyIj48cGF0aCBkPSJNMTIgMTVWM205IDEydjRhMiAyIDAgMCAxLTIgMkg1YTIgMiAwIDAgMS0yLTJ2LTQiLz48cGF0aCBkPSJtNyAxMGw1IDVsNS01Ii8%2BPC9nPjwvc3ZnPg%3D%3D&logoColor=white"></a>
</p>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/readme/hero-dark.webp">
    <img src="docs/assets/readme/hero-light.webp" alt="The Supermetry dashboard showing visitors, visits, pageviews, bounce rate, visit duration, a 30-day traffic chart, top pages and traffic sources for a sample website" width="960">
  </picture>
</p>

Supermetry is an open-source, self-hosted web analytics platform and a privacy-friendly alternative to Google Analytics. It counts visitors, pageviews, traffic sources, goals and conversions without cookies, and keeps every number in your own PostgreSQL database. Funnels, retention, user journeys, Web Vitals, heatmaps and session replay are included in the open-source release.

---

## Why Supermetry

Most analytics tools make you pick two: **privacy**, **depth**, or **easy hosting**. Supermetry gives you all three.

|  |  |
|---|---|
| 🪶 **Simple to run** | One Go binary and a PostgreSQL database. Redis is not needed. Installers for Linux, macOS and Windows, plus backup and restore commands. |
| 🔒 **Private by design** | No cookies, no fingerprinting, no raw IP addresses stored. Visitors are counted with a random daily salt that is deleted after about 48 hours. Do Not Track and Global Privacy Control are honoured. |
| ⚡ **Light on servers and pages** | The tracker is about 1.2 KB gzipped. In a 4-hour test one process ingested 200 events per second, 2.88 million events in all, with no loss, no errors, a worst-case p99 latency of 23 ms and under 190 MiB of memory ([details](tools/bench/soak/README.md)). |
| ✅ **Numbers you can check** | Side by side with Plausible Community Edition on identical real-browser traffic, every metric and breakdown matched, in both products and against hand-computed truth ([details](tools/bench/accuracy/ab/README.md)). |
| 🧭 **More than pageviews** | Goals, funnels, revenue, retention, user journeys, Web Vitals, heatmaps and session replay, all in the open-source release. No feature locks. |
| 🔓 **Yours to leave** | Apache-2.0, a REST API, an MCP endpoint for AI assistants, CSV export and imports from Google Analytics 4, Plausible, Umami, Mixpanel and Amplitude. |

## A quick tour

<p align="center">
  <img src="docs/assets/readme/tour.gif" alt="A short tour of the Supermetry dashboard: overview, realtime, user journeys, sessions, revenue and Web Vitals" width="900">
</p>

<table>
  <tr>
    <td width="50%" valign="top">
      <img src="docs/assets/readme/realtime.webp" alt="Realtime view with live visitors, visitors per minute and top pages">
      <h3>Realtime</h3>
      Who is on the site right now, visitors per minute, and the pages, sources and countries they come from. Updates every 5 seconds.
    </td>
    <td width="50%" valign="top">
      <img src="docs/assets/readme/journeys.webp" alt="User journeys diagram showing how visitors move between pages">
      <h3>User journeys</h3>
      See the paths people take from any start page, step by step, and where they leave.
    </td>
  </tr>
  <tr>
    <td width="50%" valign="top">
      <img src="docs/assets/readme/revenue.webp" alt="Revenue report with total revenue, revenue over time and revenue by source, channel and campaign">
      <h3>Revenue</h3>
      Revenue over time, attributed to the sources, channels and campaigns that brought it in.
    </td>
    <td width="50%" valign="top">
      <img src="docs/assets/readme/vitals.webp" alt="Web Vitals report with LCP, CLS, INP, FCP and TTFB at the 75th percentile and a daily trend">
      <h3>Web Vitals</h3>
      LCP, CLS, INP, FCP and TTFB from real visitors, with a daily trend and a breakdown by page, device and country.
    </td>
  </tr>
</table>

<p align="center"><sub>Screenshots show the built-in demo project. Every report works the same on your own data.</sub></p>

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
- A REST API and an MCP endpoint, CSV export, and imports from Google Analytics 4, Plausible, Umami, Mixpanel and Amplitude.

**Operations**
- PostgreSQL only, with time partitions that are dropped for retention instead of bulk deletes.
- Backup and restore commands, a protected metrics endpoint and a data health page.
- Country lookups from a bundled [DB-IP](https://db-ip.com) database, with optional region and city detail from [MaxMind GeoLite](https://www.maxmind.com).

## Quick start

Linux and macOS:

```sh
curl -fsSL https://github.com/velixio/supermetry/releases/latest/download/install.sh | sh
```

Windows PowerShell:

```powershell
irm https://github.com/velixio/supermetry/releases/latest/download/install.ps1 | iex
```

To see what the installer would do first, add `--dry-run`, for example `curl -fsSL https://github.com/velixio/supermetry/releases/latest/download/install.sh | sh -s -- --dry-run`. **Docker Compose**, with a bundled PostgreSQL:

```sh
git clone https://github.com/velixio/supermetry.git && cd supermetry
cp .env.example .env    # set SUPERMETRY_SECRET_KEY and POSTGRES_PASSWORD
docker compose -f deploy/docker-compose.yml --profile bundled-db up -d --build
```

Docker serves the dashboard on `http://localhost:8787/app`; see [Docker Compose](deploy/README.md#docker-compose-by-hand) for an external database.

Open `<your URL>/app` and create your admin account. Then add the tracking script to your site. Copy the exact snippet from **Settings, Installation** in the dashboard. It looks like this:

```html
<script defer src="https://analytics.supermetry.com/js/YOUR_SITE_ID.js"></script>
```

**Just want to look around?** Open the [live demo](https://app.supermetry.com/demo?utm_source=github&utm_medium=readme&utm_campaign=supermetry-repo&utm_content=demo-intro): no signup, a read-only dashboard for a small online shop, with live visitors, goals and campaigns, for up to 60 minutes. On your own install, choose **Explore with demo data** after you sign up. Supermetry creates a project with 90 days of realistic sample traffic and keeps it moving while you explore. It deletes itself after 24 hours, and you can remove it earlier.

See [deploy/README.md](deploy/README.md) for unattended installs, manual deployment, service management, upgrades, backups and reverse proxies, and [docs/guide/configuration.md](docs/guide/configuration.md) for every setting.

### Requirements

- PostgreSQL (18 is what CI runs). Redis is not needed; a single instance runs without it.
- Release binaries for Linux, macOS and Windows on amd64 and arm64, plus 32-bit Linux armv6/armv7 for Raspberry Pi.

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

<details>
<summary><b>Is Supermetry a Google Analytics alternative?</b></summary>

Yes. It measures visitors, pageviews, sources, goals and conversions without cookies, and the data stays in your own PostgreSQL database.
</details>

<details>
<summary><b>How is it different from Plausible or Umami?</b></summary>

Plausible's self-hosted edition needs ClickHouse next to PostgreSQL, and Umami runs on Node.js. Supermetry is a single Go binary and PostgreSQL only, and it includes funnels, revenue, retention, session replay and heatmaps in the open-source release.
</details>

<details>
<summary><b>Does it use cookies or store IP addresses?</b></summary>

It sets no cookies. IP addresses are used to look up the country (and city, if you add MaxMind data) and to derive a daily-salted visitor ID, and are not stored. See [Tracking and privacy](docs/tracking-privacy.md).
</details>

<details>
<summary><b>Do I need a cookie banner?</b></summary>

That depends on the laws that apply to you and on what else your site runs, so ask your own counsel. Supermetry is designed to need as little consent as possible: no cookies, no raw IPs and no cross-site tracking, and it honours Do Not Track and Global Privacy Control.
</details>

<details>
<summary><b>Can I move my existing data in?</b></summary>

Yes. Import from Google Analytics 4, Plausible, Umami, Mixpanel or Amplitude in the project settings, then export to CSV whenever you like.
</details>

<details>
<summary><b>What does it need to run?</b></summary>

A small server and PostgreSQL. In the 4-hour test above, a single process used under 190 MiB of memory at a sustained 200 events per second.
</details>

<details>
<summary><b>Is there a hosted version?</b></summary>

An EU-hosted Cloud is open: [start free](https://app.supermetry.com/signup?utm_source=github&utm_medium=readme&utm_campaign=supermetry-repo&utm_content=faq-signup) (the Free plan today; paid plans are coming soon) or [try the live demo](https://app.supermetry.com/demo?utm_source=github&utm_medium=readme&utm_campaign=supermetry-repo&utm_content=faq-demo). Self-hosting stays free and complete.
</details>

## Documentation

- [Configuration](docs/guide/configuration.md)
- [Tracking and privacy](docs/tracking-privacy.md)
- [API](docs/api.md) and [API authentication](docs/api-auth.md)
- [Authorization](docs/authorization.md)
- [Notifications](docs/guide/notifications.md)
- Full docs at [supermetry.com/docs](https://supermetry.com/docs/?utm_source=github&utm_medium=readme&utm_campaign=supermetry-repo&utm_content=docs-link)

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

Issues and pull requests are welcome. See [CONTRIBUTING.md](.github/CONTRIBUTING.md). Report vulnerabilities privately as described in [SECURITY.md](.github/SECURITY.md).

If Supermetry is useful to you, a ⭐ helps other people find it.

## License

[Apache-2.0](LICENSE). Third-party notices are in [NOTICE](NOTICE) and [THIRD_PARTY.md](THIRD_PARTY.md). Wallpapers behind the screenshots are credited in [docs/assets/readme/CREDITS.md](docs/assets/readme/CREDITS.md).

[![views](https://app.supermetry.com/px/zfjiv7psp4ed.svg?c=github-supermetry&style=stats)](https://supermetry.com/?utm_source=github&utm_medium=readme&utm_campaign=supermetry-repo&utm_content=views-badge)
