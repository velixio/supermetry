# Deploy Panel

Panel needs PostgreSQL and three required settings: `PANEL_DATABASE_URL`,
`PANEL_SECRET_KEY`, and `PANEL_BASE_URL`.

## Docker Compose

From this directory:

```sh
cp ../.env.example .env
# Edit .env: set PANEL_SECRET_KEY, PANEL_BASE_URL, and add POSTGRES_PASSWORD.
docker compose up -d --build
```

Compose overrides `PANEL_DATABASE_URL` and `PANEL_LISTEN` for its service
network. URL-encode special characters in `POSTGRES_PASSWORD`. Data is kept
in the `panel-data` and `postgres-data` named volumes.

## systemd

Release archives contain `panel`, `LICENSE`, `NOTICE`, and `panel.service`. Install
the binary at `/usr/local/bin/panel`, the unit at
`/etc/systemd/system/panel.service`, and configuration at
`/etc/panel/panel.env`. Then run:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now panel
```

The service runs as `panel` and stores writable data in `/var/lib/panel`.

## Upgrades

Replace the Panel binary or image and restart the service. Database migrations
run automatically at startup. When a release changes the rollup layout,
Panel rebuilds already-closed days in the background, one closed site-local
day at a time: a release that only adds breakdowns recomputes just the new
rollup dimensions, while a release that adds or changes rollup counters runs
the full per-day recompute. Startup and readiness are not delayed, queries
keep working (breakdowns for days not yet rebuilt may be incomplete), the work
resumes after a restart, and progress appears on the Data health page.

No manual rollup step is required after an upgrade. The
`panel recompute-rollups` command remains available for an explicit full
rebuild when troubleshooting or repairing rollup data.

## Backup and restore

Run `panel backup --out FILE` against the configured database. The archive is
created with mode `0600`; it contains the PostgreSQL dump and a manifest, but
not `PANEL_SECRET_KEY`, GeoIP databases, or replay files.

Stop Panel before running `panel restore FILE --yes`, and restore only into its
dedicated PostgreSQL database. Restore replaces database objects and is not
atomic, so a `pg_restore` failure can leave the target partially restored. A
binary accepts archives whose schema version is no newer than its highest
embedded migration and refuses archives created by a newer schema. After a
successful restore, restart Panel so any migrations newer than the archive run.

## Installer

Set the release asset base URL. It normally ends with the version used in the
archive name:

```sh
curl -fsSLO https://example.invalid/path/install.sh
sudo PANEL_RELEASE_URL=https://example.invalid/releases/download/v1.2.3 \
  sh install.sh --yes
```

For a piped install, `--yes` is mandatory:

```sh
curl -fsSL https://example.invalid/path/install.sh | \
  sudo PANEL_RELEASE_URL=https://example.invalid/releases/download/v1.2.3 \
  sh -s -- --yes
```

The installer verifies the archive against `SHA256SUMS` before installing
it and never overwrites an existing `/etc/panel/panel.env`.

## Session replay

Off by default. `PANEL_REPLAY_ENABLED=true` is the only switch: it turns replay on
for every site, and the dashboard shows the status read-only. Restart panel after
changing it.

- **Tracker:** sites must use the per-site snippet from Site settings → Installation
  (`/js/<site-id>.js`). The generic `/js/script.js` and `/js/script.full.js` never start replay.
- **Storage:** gzipped files in `$PANEL_DATA_DIR/replay` (systemd: `/var/lib/panel/replay`;
  Compose: the `panel-data` volume). A custom `PANEL_REPLAY_DIR` under systemd must stay
  under `/var/lib/panel`, or be added to `ReadWritePaths`.
- **S3:** set `PANEL_REPLAY_S3_ENDPOINT`, `_BUCKET`, `_REGION`, `_ACCESS_KEY` and
  `_SECRET_KEY` together (`_PREFIX` optional). Uploads collect in
  `$PANEL_DATA_DIR/replay-spill` until each session ends.
- **Limits:** 15% of sessions sampled, 30-day retention and a 5 GB quota per site by
  default (oldest deleted first). Any `PANEL_REPLAY_*` value set in the environment wins
  over the dashboard; see `.env.example`.
- **Backups:** `panel backup` leaves replay files out; back up the replay directory or
  bucket separately if you need them.
- **Reverse proxy:** replay uploads are at most 256 KB per chunk and 1 MiB per
  stylesheet, so the default `client_max_body_size 1m` is enough.

## Reverse proxy

Adapt the nginx example in [`deploy/nginx/`](nginx/). Set
`PANEL_TRUSTED_PROXIES` to include the proxy's source IP or CIDR so Panel
uses forwarded client addresses only from that trusted proxy.
