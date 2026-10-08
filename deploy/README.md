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

## Reverse proxy

Adapt the nginx example in [`deploy/nginx/`](nginx/). Set
`PANEL_TRUSTED_PROXIES` to include the proxy's source IP or CIDR so Panel
uses forwarded client addresses only from that trusted proxy.
