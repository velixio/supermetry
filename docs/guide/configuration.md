# Configuration

Supermetry is configured with `SUPERMETRY_*` environment variables. On start, `supermetry` reads `./.env` (or the file named by `SUPERMETRY_ENV_FILE`). A variable already set in the real environment wins over the file. A missing file is not an error. Lines are `KEY=VALUE`; `#` comments, an `export ` prefix and surrounding quotes are accepted (`internal/config/dotenv.go`).

Invalid values stop startup with an error naming the variable (`internal/config/config.go`).

| Variable | Purpose |
|---|---|
| `SUPERMETRY_ENV_FILE` | Path of the env file to read. Default `.env`. |

## Required

`supermetry serve` will not start without these three.

| Variable | Meaning | Default | Example |
|---|---|---|---|
| `SUPERMETRY_DATABASE_URL` | PostgreSQL connection URL. Must parse as a URL with a scheme and host. | none (required) | `postgres://supermetry:CHANGE_ME@127.0.0.1:5432/supermetry` |
| `SUPERMETRY_SECRET_KEY` | Base64 (standard encoding) of at least 32 random bytes. Encrypts stored TOTP secrets and alert secrets. | none (required) | output of `openssl rand -base64 32` |
| `SUPERMETRY_BASE_URL` | Public URL of the install, `http` or `https`. Used to build the tracking snippet (absolute script and event URLs). | none (required) | `https://analytics.example.com` |
| `SUPERMETRY_HOMEPAGE_URL` | Public brand link used by “Powered by Supermetry”. It never changes auth, API, tracker, email-link, or cookie origins. | `https://supermetry.com` | `https://company.example/analytics` |

Gotchas:

- Use an `https` base URL in production. Supermetry then sets the `Secure` flag on every cookie even if a proxy omits `X-Forwarded-Proto` (`internal/platform/http/private.go`).
- The dashboard entry is `/app`. Organization and site routes remain same-origin. Do not use `SUPERMETRY_HOMEPAGE_URL` as the instance origin; it is an ordinary external link only.
- Losing or changing `SUPERMETRY_SECRET_KEY` makes stored TOTP secrets unreadable (see below).

## Server

| Variable | Meaning | Default | Example |
|---|---|---|---|
| `SUPERMETRY_LISTEN` | Address the HTTP server listens on. | `:8080` | `127.0.0.1:8787` |
| `SUPERMETRY_TRUSTED_PROXIES` | Comma-separated IPs or CIDR prefixes of reverse proxies whose `X-Forwarded-For` is trusted. A bare IP means a single-host prefix. | empty (no proxy trusted) | `127.0.0.1/32,10.0.0.5` |
| `SUPERMETRY_SIGNUP` | Signup mode: `open` or `invite`. Any other value is rejected. | `open` | `invite` |
| `SUPERMETRY_ALLOW_PRIVATE_WEBHOOKS` | Boolean (Go `ParseBool`: `1`, `t`, `true`, `0`, `f`, `false`, ...). Passed to the alert webhook client as "allow private". Exact effect: see source, `internal/alerts` and `cmd/supermetry/serve.go`. | `false` | `false` |

Notification destinations, including Telegram, are configured in the application rather than through environment variables. See [Notification channels](notifications.md) for routing, secret handling, testing, and Telegram setup.

Gotcha: the default `open` signup lets anyone who can reach the server register. Set `invite` on a public install if that is not what you want.

### How SUPERMETRY_TRUSTED_PROXIES works

Supermetry only believes forwarding headers when the TCP peer address is inside `SUPERMETRY_TRUSTED_PROXIES` (`internal/ingest/diag.go`, `proxyTrusted`). Behind a reverse proxy:

1. List the address the proxy connects from, for example `127.0.0.1/32` for nginx on the same host. If it is not listed, Supermetry sees the proxy's private address as the visitor and the visitor country is empty.
2. The proxy must overwrite `X-Forwarded-For` with the real client address, not append to it. In nginx:

```nginx
proxy_set_header X-Forwarded-For $remote_addr;
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-Proto https;
proxy_set_header X-Forwarded-Host $host;
```

A full example is in the nginx example in `deploy/nginx/`. Its header comment suggests `SUPERMETRY_LISTEN=127.0.0.1:8787` and `SUPERMETRY_TRUSTED_PROXIES=<nginx source IP>/32`. If a CDN or another proxy sits in front of nginx, `$remote_addr` is that proxy's address; see source (`internal/ingest/`) for which headers are read.

To check the setup, set `SUPERMETRY_INGEST_DIAG=1` (below) and look at `peer_trusted` and `class` in the log.

## Tracking

| Variable | Meaning | Default | Example |
|---|---|---|---|
| `SUPERMETRY_EVENT_PATH` | URL path the tracker posts events to. Must start with `/`. | `/api/public/v1/events` | `/a/e` |
| `SUPERMETRY_TRACKER_PATH` | URL path the tracker script is served from. Must start with `/`. | `/js/script.js` | `/a/t.js` |

Ad blockers match on well-known names such as `script.js` or `/events`. Changing both paths to neutral values avoids those filters. The tracking snippet shown in the dashboard is built from `SUPERMETRY_BASE_URL` plus these paths, so reinstall the snippet on your sites after changing them. Whether the old paths keep working after a change: see source (`internal/api/router.go`). If a proxy routes only specific paths to Supermetry, update it to match.

## GeoIP

GeoIP is required to start. There are two modes (`cmd/supermetry/geoip.go`).

Mode 1: no MaxMind account configured. Supermetry loads `SUPERMETRY_GEOIP_PATH` if set. With no path, it uses the DB-IP Lite country database bundled in the binary (`internal/enrich/geo.go`). Country only.

Mode 2: `SUPERMETRY_MAXMIND_ACCOUNT_ID` is set. At startup Supermetry:

1. creates `SUPERMETRY_DATA_DIR` with mode `0700`;
2. looks for `<DATA_DIR>/<edition>.mmdb` for each edition and validates each file that exists. A corrupt or wrong-edition file stops startup;
3. downloads each missing edition from MaxMind. If a download fails, startup fails;
4. loads the databases.

While running, a `geoip-update` job runs every 24 hours (`geoIPUpdateInterval`). If a background refresh fails, Supermetry logs a warning and keeps serving. The `supermetry geoip` subcommand also uses these variables.

| Variable | Meaning | Default | Example |
|---|---|---|---|
| `SUPERMETRY_GEOIP_PATH` | Path to a MaxMind-format country database. In Mode 2 it is used as the country database. | empty (bundled DB-IP Lite) | `/var/lib/supermetry/country.mmdb` |
| `SUPERMETRY_DATA_DIR` | Directory for downloaded MaxMind databases. | `./data` | `/var/lib/supermetry` |
| `SUPERMETRY_MAXMIND_ACCOUNT_ID` | MaxMind account ID. Must be set together with the license key. | empty | `123456` |
| `SUPERMETRY_MAXMIND_LICENSE_KEY` | MaxMind license key. Must be set together with the account ID. Treat as a secret. | empty | `YOUR_LICENSE_KEY` |
| `SUPERMETRY_MAXMIND_EDITIONS` | Comma-separated editions. Allowed: `GeoLite2-City`, `GeoLite2-ASN`, `GeoLite2-Country`. Duplicates and unknown names are rejected. | `GeoLite2-City,GeoLite2-ASN` | `GeoLite2-City` |

Gotchas:

- Setting only one of account ID and license key is an error.
- The data directory must be writable by the Supermetry user and kept across restarts, or Supermetry downloads the databases on every start.
- With City data, Supermetry reports region and city; with ASN data, the network. With Country data only, you get country.

## Ingest tuning

Defaults suit most installs. Durations use Go syntax (`500ms`, `2s`, `1m`) and must be positive.

| Variable | Meaning | Default | Example |
|---|---|---|---|
| `SUPERMETRY_QUEUE_SIZE` | Capacity of the in-memory event queue; positive integer. Behavior when full: see source (`internal/ingest`, `internal/writer`). | `65536` | `131072` |
| `SUPERMETRY_FLUSH_EVERY` | How often queued events are flushed to PostgreSQL. | `1s` | `2s` |
| `SUPERMETRY_SESSION_FLUSH` | How often session state is flushed. | `10s` | `30s` |
| `SUPERMETRY_QUERY_TIMEOUT` | Query timeout setting. It is parsed and validated, but no use was found outside the config package; see source (`internal/config/config.go`, `internal/query/guard.go`). | `10s` | `15s` |

## Email / SMTP

Leave `SUPERMETRY_SMTP_HOST` empty to configure no mail server. How Supermetry behaves without it: see source (`internal/config`, `cmd/supermetry/serve.go`).

| Variable | Meaning | Default | Example |
|---|---|---|---|
| `SUPERMETRY_SMTP_HOST` | SMTP server host. | empty | `smtp.example.com` |
| `SUPERMETRY_SMTP_PORT` | Port, 1 to 65535. | `587` | `587` |
| `SUPERMETRY_SMTP_USER` | SMTP username. | empty | `supermetry@example.com` |
| `SUPERMETRY_SMTP_PASS` | SMTP password. Treat as a secret. | empty | `CHANGE_ME` |
| `SUPERMETRY_SMTP_FROM` | Sender address. Required whenever `SUPERMETRY_SMTP_HOST` is set. | empty | `supermetry@example.com` |
| `SUPERMETRY_SMTP_STARTTLS` | Boolean; use STARTTLS. | `true` | `true` |

## Development and benchmark only

Do not set these on a normal install.

| Variable | Meaning | Default | Example |
|---|---|---|---|
| `SUPERMETRY_ADMIN_API_KEY` | A static API key. Used as a fallback key in compatibility mode, and as the public-API key in the router (`cmd/supermetry/serve.go`, `internal/api/router.go`). Exact precedence: see source. | empty | `CHANGE_ME` |
| `SUPERMETRY_SITES_FILE` | Path to a sites file. `supermetry load` requires it. `supermetry serve` uses it only when no platform user exists yet, and logs a warning. | empty | `bench/sites.json` |
| `SUPERMETRY_INGEST_DIAG` | Set to exactly `1` for temporary ingest diagnostics. Each event logs an "ingest ip diag" line with address class (loopback, private, link-local, public, ...), a masked peer address, whether the peer is trusted, masked `X-Forwarded-For`, `X-Real-IP`, `CF-Connecting-IP` and `Forwarded` headers, and the country. Public addresses are never logged. | unset | `1` |

Turn `SUPERMETRY_INGEST_DIAG` off after debugging, since it logs once per event.

Tests also read `SUPERMETRY_TEST_DSN` (a PostgreSQL DSN for the test suite; tests skip when it is unset). It is not used by the server.

## Generating SUPERMETRY_SECRET_KEY

```sh
openssl rand -base64 32
```

Put the output in `SUPERMETRY_SECRET_KEY`. Back it up outside the server. The key encrypts stored TOTP secrets (and alert secrets). If you lose it or change it, existing TOTP secrets cannot be decrypted, so affected users cannot pass two-factor sign-in until you reset their TOTP. How to reset it: see source (`cmd/supermetry/user.go`).

## Minimal .env

Placeholders only. Replace every value.

```sh
SUPERMETRY_DATABASE_URL=postgres://supermetry:CHANGE_ME@127.0.0.1:5432/supermetry
SUPERMETRY_SECRET_KEY=PASTE_OUTPUT_OF_openssl_rand_-base64_32
SUPERMETRY_BASE_URL=https://analytics.example.com

SUPERMETRY_LISTEN=127.0.0.1:8787
SUPERMETRY_TRUSTED_PROXIES=127.0.0.1/32
SUPERMETRY_SIGNUP=invite

# Optional: MaxMind databases (otherwise the bundled country database is used)
# SUPERMETRY_MAXMIND_ACCOUNT_ID=YOUR_ACCOUNT_ID
# SUPERMETRY_MAXMIND_LICENSE_KEY=YOUR_LICENSE_KEY
# SUPERMETRY_DATA_DIR=/var/lib/supermetry
```

Never commit your real `.env`.
