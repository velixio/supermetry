# Deploy Supermetry

Supermetry is one binary backed by PostgreSQL. The installer is the recommended
path; the manual paths below are available when you need complete control.

## Install with the installer

Linux and macOS:

```sh
curl -fsSL https://github.com/velixio/supermetry/releases/latest/download/install.sh | sh
```

Windows PowerShell:

```powershell
irm https://github.com/velixio/supermetry/releases/latest/download/install.ps1 | iex
```

The installer asks for the deployment mode (Docker or binary), public URL,
PostgreSQL connection, listen address, reverse proxy, and whether to download or
build the Docker image. Docker image downloads are the default. Reverse proxy
setup defaults to none, and the recommended listen address is
`127.0.0.1:8787`. On macOS and Windows, a binary install writes `.env` and prints
the command to start Supermetry; it does not create a service.

For unattended use, pass `--yes` and every required value. Available flags are:

- `--mode docker|binary`
- `--base-url`
- `--database-url`, or `--db-host`, `--db-port`, `--db-user`, `--db-password`, `--db-name`, and `--db-sslmode`
- `--listen`
- `--proxy nginx|caddy|none`
- `--image download|build`
- `--dir`
- `--yes`

With `--yes`, omitted required values and a failed database check are errors.
Without a TTY, the piped shell installer also requires `--yes`. The binary path
tests PostgreSQL before it writes files or creates a user or service.

When installation finishes, open `<your URL>/app` and create the admin account.

## Docker Compose by hand

From a repository checkout, copy and edit the example configuration:

```sh
cp .env.example .env
${EDITOR:-vi} .env
```

Set `SUPERMETRY_BASE_URL` and `SUPERMETRY_SECRET_KEY`. For an external database,
also set `SUPERMETRY_DATABASE_URL`, then start the application:

```sh
docker compose -f deploy/docker-compose.yml up -d --build
```

Inside the container, `localhost` is the container itself. Use
`host.docker.internal` to reach PostgreSQL on the Docker host. The database must
accept connections from the Docker network.

To use the included `postgres:18-alpine` service instead, leave
`SUPERMETRY_DATABASE_URL` commented out, set `POSTGRES_USER`,
`POSTGRES_PASSWORD`, and `POSTGRES_DB` in `.env`, and run:

```sh
docker compose -f deploy/docker-compose.yml --profile bundled-db up -d --build
```

The database is not published outside the Compose network. Application data is
stored in `supermetry_supermetry-data`; PostgreSQL data is stored in
`supermetry_postgres-data`. The application binds to `127.0.0.1:8787` by
default.

## Binary and systemd by hand

Install PostgreSQL first and create a dedicated database. From an extracted
Linux release archive, install the user, directories, binary, and unit:

```sh
getent group supermetry >/dev/null || sudo groupadd --system supermetry
getent passwd supermetry >/dev/null || sudo useradd --system --gid supermetry --home-dir /var/lib/supermetry --shell /usr/sbin/nologin supermetry
sudo install -d -m 0750 -o root -g supermetry /etc/supermetry
sudo install -d -m 0700 -o supermetry -g supermetry /var/lib/supermetry
sudo install -m 0755 supermetry /usr/local/bin/supermetry
sudo install -m 0644 supermetry.service /etc/systemd/system/supermetry.service
sudoedit /etc/supermetry/supermetry.env
sudo chown root:supermetry /etc/supermetry/supermetry.env
sudo chmod 0640 /etc/supermetry/supermetry.env
```

Set `SUPERMETRY_DATABASE_URL`, `SUPERMETRY_SECRET_KEY`,
`SUPERMETRY_BASE_URL`, and `SUPERMETRY_LISTEN` in the environment file. Generate
a secret with:

```sh
openssl rand -base64 32
```

The unit runs as `supermetry`, stores writable data in `/var/lib/supermetry`,
and must contain `ExecStart=/usr/local/bin/supermetry`. Start it with:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now supermetry
```

## Manage the service

```sh
sudo systemctl start supermetry
sudo systemctl stop supermetry
sudo systemctl restart supermetry
sudo systemctl enable supermetry
sudo systemctl disable supermetry
sudo systemctl status supermetry
sudo journalctl -u supermetry -f
```

## Email (SMTP), optional

Email enables invitations, password resets, and reports. Edit the install's
`.env` file and fill in `SUPERMETRY_SMTP_HOST`, `SUPERMETRY_SMTP_PORT`,
`SUPERMETRY_SMTP_USER`, `SUPERMETRY_SMTP_PASS`, and `SUPERMETRY_SMTP_FROM`.
`SUPERMETRY_SMTP_STARTTLS` defaults to `true`. Then restart Supermetry:

```sh
sudoedit /etc/supermetry/supermetry.env
sudo systemctl restart supermetry
```

For Docker, edit `.env` in the install directory and run:

```sh
${EDITOR:-vi} .env
docker compose restart supermetry
```

## Upgrade

Run the installer again. It replaces the binary or image and restarts the
service while keeping the existing configuration and secret key.

Linux and macOS:

```sh
curl -fsSL https://github.com/velixio/supermetry/releases/latest/download/install.sh | sh
```

Windows PowerShell:

```powershell
irm https://github.com/velixio/supermetry/releases/latest/download/install.ps1 | iex
```

Database migrations run automatically at startup.

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

## Reverse proxy

Keep Supermetry on `127.0.0.1:8787` when a proxy runs on the same host. For a
binary install, set `SUPERMETRY_TRUSTED_PROXIES=127.0.0.1`. For Docker, use the
proxy's source address on the Compose network. Restart Supermetry after changing
the setting.

For nginx, install and edit the example at the required path, test it, and
reload:

```sh
sudo install -m 0644 deploy/nginx/supermetry.example.conf /etc/nginx/conf.d/supermetry.conf
sudoedit /etc/nginx/conf.d/supermetry.conf
sudo nginx -t
sudo systemctl reload nginx
```

For Caddy, add a site block to `/etc/caddy/Caddyfile`:

```sh
sudoedit /etc/caddy/Caddyfile
```

```caddyfile
analytics.example.com {
    reverse_proxy 127.0.0.1:8787
}
```

Validate and reload it:

```sh
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

Caddy manages HTTPS automatically. The installer configures either proxy when
selected, but never installs proxy packages.

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

## Troubleshooting

**`compose build requires buildx 0.17 or later`:** newer Compose versions hand
builds to BuildKit's `bake`, which needs buildx 0.17 or later. Upgrade the
buildx plugin, or turn bake off for this run:

```sh
COMPOSE_BAKE=false docker compose -f deploy/docker-compose.yml up -d --build
```

Running `docker compose` (the plugin) instead of the standalone
`docker-compose` binary can also avoid the error when the plugin is newer.

**Old `deploy-*` resources:** Compose now uses the fixed project name
`supermetry`. Older resources may be named `deploy-*` and `deploy_*`. Inspect
them before removing anything:

```sh
docker ps -a --filter name=deploy-
docker volume ls --filter name=deploy_
```

After copying or backing up any data you need, remove the old container:

```sh
docker rm -f deploy-supermetry-1
```

**PostgreSQL 18 volume path:** PostgreSQL 18 stores its cluster below
`/var/lib/postgresql/18/docker`, so the Compose file mounts `postgres-data` at
`/var/lib/postgresql`. If an older Supermetry Compose file mounted the volume at
`/var/lib/postgresql/data`, back up the database while the old configuration is
running and restore it into a fresh PostgreSQL 18 volume. Do not remount the old
volume at the new path and assume PostgreSQL will find it.
