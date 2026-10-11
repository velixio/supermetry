# Supermetry

Privacy-friendly, self-hosted web analytics. One Go binary and PostgreSQL. No ClickHouse, no Kafka, no Node in production.

> Status: pre-release. Expect breaking changes until `v0.1.0`.

## Features

- Cookie-free pageview and custom-event tracking with a small script.
- No raw IPs stored and no fingerprinting. Visitor IDs come from a random daily salt that is deleted after about 48 hours.
- Goals, funnels, revenue, UTM and referrer attribution, retention, Web Vitals, heatmaps and session replay.
- Dashboard, REST API and MCP endpoint, with API keys and shareable links.
- Teams, roles, two-factor sign-in, alerts and webhooks.
- Imports from Plausible and Umami, plus CSV export.
- Retention by dropping time partitions, backup and restore commands.

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

Open `<your URL>/app` and create your admin account. See [deploy/README.md](deploy/README.md) for unattended installs, manual deployment, service management, upgrades, backups, and reverse proxies. See [docs/guide/configuration.md](docs/guide/configuration.md) for every setting.

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

## Documentation

- [Configuration](docs/guide/configuration.md)
- [Tracking and privacy](docs/tracking-privacy.md)
- [API](docs/api.md) and [API authentication](docs/api-auth.md)
- [Authorization](docs/authorization.md)
- [Notifications](docs/guide/notifications.md)

## Development

You need Go, [Bun](https://bun.sh) and Docker. No database settings are needed: unless `.env` sets
`SUPERMETRY_DATABASE_URL` (or `SUPERMETRY_TEST_DSN` for tests), `make` starts PostgreSQL in Docker on
port 5433 and uses it. `make dev-db-down` stops it; its data is kept.

```sh
make dev     # build and run the server
make test    # Go tests
make lint    # go vet and gofmt
```

Dashboard code is in `web/` and uses Bun (`bun install --frozen-lockfile`, `bun run test`).

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md). Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## License

[Apache-2.0](LICENSE). Third-party notices are in [NOTICE](NOTICE) and [THIRD_PARTY.md](THIRD_PARTY.md).
