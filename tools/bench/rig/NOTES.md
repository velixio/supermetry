# tools/bench/rig notes (rig agent)

Owner: rig agent. Scope: Docker, the three incumbents, up/down/status, PG tuning profile.
The measured-numbers report `incumbents-setup.md` was removed 2026-10-10 (in git at `7e7294d`); headline numbers are in progress.md.

## Layout

| Path | What |
|---|---|
| `rig.conf` | Pinned versions and ports (not secret). Sourced by every script; phase-B loaders can source it too. |
| `rig.local.conf` | Machine-local port overrides (see "Ports" below). |
| `.env` (600) | **All** secrets and admin credentials. `init-env.sh` creates it once with random values. |
| `plausible/ce/` | The official `plausible/community-edition` clone at `v3.2.1`, untouched, plus the README-style `.env` (600, derived from `../.env`) and `compose.override.yml`, both written by `up.sh`. |
| `run/umami.env` (600) | Umami container env, derived from `.env` by `up.sh`. |
| `bin/goatcounter` | → `goatcounter-v2.7.0` (release binary). |
| `*-sites.json` | `[{"site":N,"host":"siteN.test","id":<product id>}]` (GoatCounter adds `user_id`). |
| `logs/`, `run/` | GoatCounter log and pid; O2 run log. |

## Scripts

All of them take `plausible|umami|goatcounter|all` and are idempotent.

- `up.sh P`: installs on first use (CE clone + images / role+db / binary download), starts, and waits for health.
- `down.sh P [--purge|--cold]`: stops. `--purge` also drops the data (compose volumes, or the local PG db+role) and the mapping. `--cold` additionally removes the images, the CE clone and the binary.
- `status.sh P`: state, port, site count and health. Exits 0 only if all requested products are up.
- `seed.sh P [N]`: creates the admin and `site1..N.test` (UTC), then writes `P-sites.json`.
- `pageview.sh P [site]`: sends one pageview through the real tracking API and polls storage until it's visible. Prints JSON timings.
- `sync-timezones.sh <gen sites.json> [P]`: sets each site's zone (see per-product notes). Tested with a synthetic 5-zone file, then reset to UTC.
- `o2-install.sh P --cold|--warm`: the timed O2 run, which writes `../results/o2-P-mode-N.json`.
- `r1-idle.sh [secs]`: idles, then measures memory, writing `../results/r1-idle.json`. It uses `memstat.py`, run as root, for /proc RSS/PSS.
- `pg-tuning.conf` and `apply-pg-tuning.sh`: the profile is **not applied**. With no flag the script is a read-only dry run; `--apply` and `--revert` restart PG.

## Decisions and facts (2026-10-07)

- **Docker**: `docker.io` 26.1.5 and `docker-compose` 2.26.1 from Debian. `docker compose version` works. The user is **not** in the `docker` group (that's a system change and needs a re-login), so `lib.sh` falls back to `sudo docker`.
- **Ports**: the contract ports :3000 and :8081 are taken by another project's dev servers (vite on :3000 and :3001, `dashboard-api` on 127.0.0.1:8081, an `admin` binary on :8082). They weren't stopped. **Umami runs on :3030 and GoatCounter on :8083** (`rig.local.conf`); delete those overrides once the contract ports are free. Plausible is on :8000 as contracted. Everything binds to 127.0.0.1.
- **No jq** on the box, and installing it would be a system change, so JSON goes through `python3 -I`.
- **Plausible CE v3.2.1**: the newest v3.x branch of plausible/community-edition (2026-05-15; matches the scorecard's "v3.2.x"). The compose is used verbatim, including its low-resources ClickHouse profile (`max_threads=1`, etc.); that **is** CE's default, so the Speed numbers have to keep it. The README route is `.env` with `BASE_URL=http://localhost:8000`, `SECRET_KEY_BASE` and `HTTP_PORT=80`, plus an override mapping `127.0.0.1:8000:80`.
  - Admin and sites go through **release RPC** (`/app/bin/plausible rpc`; note that `/entrypoint.sh rpc` doesn't exist), using `Plausible.Auth.User.new/1` + `Repo.insert!` and then `Plausible.Sites.create(user, %{"domain","timezone"})`. The Elixir is piped on stdin so the password stays out of the host argv. Email verification is off by default in CE, so the user is verified.
  - The CE compose's PG/CH healthchecks set no `interval`, so Docker's 30 s default delays the app's start (`depends_on: service_healthy`) by about 30 s on every `up`. This is part of CE's default; I left it alone.
  - A brand-new site is **dropped** by `/api/event` (`x-plausible-dropped` header) until the site cache's 30 s "recently updated" refresh. `pageview.sh` retries, and the wait counts towards O2.
  - Storage DBs: PG `plausible_db`, ClickHouse `plausible_events_db`. `site_id` comes from `plausible-sites.json`.
  - The Stats API needs an API key, which hasn't been created yet; phase B can create one via RPC.
- **Umami 3.4.0**: the image is `ghcr.io/umami-software/umami:3.4.0`. v3 is PG-only, and the `postgresql-` tag prefix stopped after 3.0.0. It runs with `--network host --init --restart unless-stopped`, with env as in Umami's own compose (`DATABASE_URL`, `APP_SECRET`, `TWO_FACTOR_ENCRYPTION_KEY`) plus `PORT`/`HOSTNAME`. The image runs `prisma migrate deploy` (26 migrations) on start.
  - The migration seeds `admin/umami`; `seed.sh` rotates it via `POST /api/me/password` (verified: the old password is rejected). Websites are created via `POST /api/websites`.
  - Umami has **no per-website time zone**: the query harness must pass `timezone=` on each stats call, so `sync-timezones.sh` is a no-op for Umami.
  - Its PG pool closes idle connections, so at idle there can be 0 backends.
- **GoatCounter v2.7.0** (2025-12-15, the latest release). The GitHub release CDN speed varies: 50 s for 14 MB once, about 1 s later. The binary is **not** checksum-verified, because the release publishes no checksums.
  - `goatcounter db migrate all -createdb` initializes the empty db that I pre-create; `db newdb` fails on an existing empty PG db in v2.7.0.
  - It treats any `GOATCOUNTER_*` env var as a flag and **refuses unknown ones**, so `lib.sh` doesn't export `.env`.
  - Sites are matched by `Host` header = `sites.cname`. Loaders must send `Host: siteN.test` to 127.0.0.1:8083.
  - **The time zone is a per-user setting**, as `users.settings.timezone = "CC.Area/City"` (".UTC" for UTC). So seed creates 100 independent sites, each with its own user (same email and password). `sync-timezones.sh` updates `users.settings` and restarts serve. Stats are stored in UTC hourly buckets, so +5:30 and +5:45 can't be exact (INFO for C1).
  - The default site setting `collect=190` has **no `CollectHits`**: individual pageviews are *not* stored, only the aggregates (`hit_counts`, `hit_stats`, …). This matters for R3 and P1. `pageview.sh` checks `hit_counts`.
  - Hits persist every 10 s (`-store-every`), which dominates its O2 time.
  - The default **rate limit is `count:4/1` (4 req/s per IP)**. Phase B's S1 ingest test needs `-ratelimit count:…` raised, or many source IPs (note in results).
- **Local PG**: `ensure_pg_db` creates role+db `umami` and `goatcounter` (SCRAM password, over TCP to localhost). `down.sh --purge` drops them. Other apps' databases on this server (cryptoscan, pay, keeper, superadmin) are untouched.
- **PG tuning**: `pg-tuning.conf` goes in as a `conf.d/90-supermetry-bench.conf` drop-in (Debian's `include_dir = 'conf.d'` is active). `shared_buffers` and `max_worker_processes` need a **restart**, and a restart drops the other local apps' connections too. Not applied.

## State at hand-off (2026-10-07)

All three products are **stopped** (`down.sh all`, data kept: 100 sites each, all UTC). `up.sh all` takes about 48 s. The PG tuning profile is not applied.

## Open items / for phase B

- Plausible history load: insert straight into ClickHouse `events_v2`/`sessions_v2` (scorecard). site_id is in `plausible-sites.json`.
- Plausible Stats API key: create via RPC when the query harness needs it.
- GoatCounter: decide whether to enable `CollectHits` (it's off by default, i.e. no raw rows) and raise `-ratelimit` for S1. Both are departures from defaults, so record them.
- Swap Umami and GoatCounter back to :3000/:8081 if the another-project dev servers go away.
