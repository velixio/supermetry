# Deploy Supermetry

Supermetry needs PostgreSQL and three required settings: `SUPERMETRY_DATABASE_URL`,
`SUPERMETRY_SECRET_KEY`, and `SUPERMETRY_BASE_URL`.

The dashboard entry is `/app`. The UI links “Powered by Supermetry” to
`SUPERMETRY_HOMEPAGE_URL`, which defaults to `https://supermetry.com`. That value
does not affect same-instance authentication, API, tracker, cookies, or email links.

## Docker Compose

Compose reads its settings from the `.env` file in the repository root (`../.env`), the same file the binary uses:

```sh
cp ../.env.example ../.env
# Edit ../.env: set SUPERMETRY_SECRET_KEY, SUPERMETRY_BASE_URL and SUPERMETRY_DATABASE_URL.
docker compose up -d --build
```

The service uses `SUPERMETRY_DATABASE_URL` from that file as it is. Inside the container, `localhost` and `127.0.0.1` are the container itself, so to reach PostgreSQL on the Docker host write `host.docker.internal` (Compose maps it for you), for example `postgres://user:pass@host.docker.internal:5432/supermetry`. The database must accept connections from the Docker network.

**No PostgreSQL yet?** Let Compose run one. Choose a password, put it in `../.env` twice, and add the `bundled-db` profile:

```sh
# ../.env
POSTGRES_PASSWORD=choose-a-password
SUPERMETRY_DATABASE_URL=postgres://supermetry:choose-a-password@postgres:5432/supermetry?sslmode=disable
```

```sh
docker compose --profile bundled-db up -d --build
```

The `postgres` container reads the same `../.env`, so it also receives the other `SUPERMETRY_*` values. They are harmless to it, but keep the file private. URL-encode special characters in the password inside the URL. The database is kept in the `postgres-data` volume.

Compose uses the fixed project name `supermetry`, so the containers are `supermetry` and `supermetry-postgres`, the image is `supermetry`, and the volumes are `supermetry_supermetry-data` and `supermetry_postgres-data`. If you started an older version of this file from `deploy/`, its resources are called `deploy-*` and `deploy_*`; remove the old container with `docker rm -f deploy-supermetry-1`, and copy or discard the old volumes (`docker volume ls`) before starting the new one.

**`compose build requires buildx 0.17 or later`:** newer Compose versions hand builds to BuildKit's `bake`, which needs buildx 0.17 or later. Either upgrade the buildx plugin, or turn bake off for this run with `COMPOSE_BAKE=false docker compose up -d --build`. Running `docker compose` (the plugin) instead of the standalone `docker-compose` binary can also avoid it when the plugin is the older version.

Compose overrides `SUPERMETRY_LISTEN` for its service network. Application data is kept
in the `supermetry-data` named volume. The application port
binds to `127.0.0.1` by default; put a reverse proxy on the same host in front of
it. To expose Supermetry directly, change the port mapping to `8787:8787` (or
`0.0.0.0:8787:8787`) and apply an appropriate firewall policy.

PostgreSQL 18 stores its cluster below `/var/lib/postgresql/18/docker`, so this
Compose file mounts the volume at `/var/lib/postgresql`. If upgrading from an
older Supermetry Compose file that mounted `postgres-data` at
`/var/lib/postgresql/data`, back up the database while the old configuration is
running, then restore that backup into a fresh PostgreSQL 18 volume. Do not
remount the existing volume at the new path and assume PostgreSQL will find it.

## systemd

Release archives contain `supermetry`, `LICENSE`, `NOTICE`, and `supermetry.service`.
Install PostgreSQL 18 and its client tools on the host first. Then install
the binary at `/usr/local/bin/supermetry`, the unit at
`/etc/systemd/system/supermetry.service`, and configuration at
`/etc/supermetry/supermetry.env`. Then run:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now supermetry
```

The service runs as `supermetry` and stores writable data in `/var/lib/supermetry`.

## Upgrades

Replace the Supermetry binary or image and restart the service. Database migrations
run automatically at startup. When a release changes the rollup layout,
Supermetry rebuilds already-closed days in the background, one closed site-local
day at a time: a release that only adds breakdowns recomputes just the new
rollup dimensions, while a release that adds or changes rollup counters runs
the full per-day recompute. Startup and readiness are not delayed, queries
keep working (breakdowns for days not yet rebuilt may be incomplete), the work
resumes after a restart, and progress appears on the Data health page.

No manual rollup step is required after an upgrade. The
`supermetry recompute-rollups` command remains available for an explicit full
rebuild when troubleshooting or repairing rollup data.

## Backup and restore

Run `supermetry backup --out FILE` against the configured database. The archive is
created with mode `0600`; it contains the PostgreSQL dump and a manifest, but
not `SUPERMETRY_SECRET_KEY`, GeoIP databases, or replay files.

Stop Supermetry before running `supermetry restore FILE --yes`, and restore only into its
dedicated PostgreSQL database. Restore replaces database objects and is not
atomic, so a `pg_restore` failure can leave the target partially restored. A
binary accepts archives whose schema version is no newer than its highest
embedded migration and refuses archives created by a newer schema. After a
successful restore, restart Supermetry so any migrations newer than the archive run.

## Installer

Set the release asset base URL. It normally ends with the version used in the
archive name:

```sh
curl -fsSLO https://supermetry.com/install.sh
sudo SUPERMETRY_RELEASE_URL=https://github.com/velixio/supermetry/releases/download/v0.1.0 \
  sh install.sh --yes
```

For a piped install, `--yes` is mandatory:

```sh
curl -fsSL https://supermetry.com/install.sh | \
  sudo SUPERMETRY_RELEASE_URL=https://github.com/velixio/supermetry/releases/download/v0.1.0 \
  sh -s -- --yes
```

The installer verifies the archive against `SHA256SUMS` before installing
it and never overwrites an existing `/etc/supermetry/supermetry.env`.

## Session replay

Off by default. `SUPERMETRY_REPLAY_ENABLED=true` is the only switch: it turns replay on
for every site, and the dashboard shows the status read-only. Restart supermetry after
changing it.

- **Tracker:** sites must use the per-site snippet from Site settings → Installation
  (`/js/<site-id>.js`). The generic `/js/script.js` and `/js/script.full.js` never start replay.
- **Storage:** gzipped files in `$SUPERMETRY_DATA_DIR/replay` (systemd: `/var/lib/supermetry/replay`;
  Compose: the `supermetry-data` volume). A custom `SUPERMETRY_REPLAY_DIR` under systemd must stay
  under `/var/lib/supermetry`, or be added to `ReadWritePaths`.
- **S3:** set `SUPERMETRY_REPLAY_S3_URL=https://ACCESS_KEY:SECRET_KEY@host/bucket[/prefix]?region=REGION`
  (percent-encode special characters in the keys). The older separate `SUPERMETRY_REPLAY_S3_ENDPOINT`,
  `_BUCKET`, `_REGION`, `_ACCESS_KEY`, `_SECRET_KEY` and `_PREFIX` variables still work. Uploads collect in
  `$SUPERMETRY_DATA_DIR/replay-spill` until each session ends.
- **Limits:** 15% of sessions sampled, 30-day retention and a 5 GB quota per site by
  default (oldest deleted first). Any `SUPERMETRY_REPLAY_*` value set in the environment wins
  over the dashboard; see `.env.example`.
- **Backups:** `supermetry backup` leaves replay files out; back up the replay directory or
  bucket separately if you need them.
- **Reverse proxy:** replay uploads are at most 256 KB per chunk and 1 MiB per
  stylesheet, so the default `client_max_body_size 1m` is enough.

## Reverse proxy

Adapt the nginx example in [`deploy/nginx/`](nginx/). Set
`SUPERMETRY_TRUSTED_PROXIES` to include the proxy's source IP or CIDR so Supermetry
uses forwarded client addresses only from that trusted proxy.
