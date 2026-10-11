# Easy install: design

Date: 2026-10-11. Status: proposed.

## Goal

A first install takes one command and a few answers. Nobody has to make up a secret key or write a connection URL by hand, and nothing is written to the system until the database settings are known to work. Models: Umami's install page and OpenPanel's `./setup` quickstart.

## Decisions (user, 2026-10-11)

- Binaries come from public GitHub Releases. The repo goes public on 2026-10-11.
- The installer asks: Docker or binary.
- Docker uses our own image plus `postgres:18-alpine`.
- The binary path runs a full service setup on Linux: systemd, a reverse proxy and a readiness check. macOS and Windows get the binary and `.env` only.
- The reverse proxy is the user's choice: nginx, Caddy or none.
- The listen address is asked for. Localhost is the default and the recommended choice.
- If the database test fails, the user edits the values they already entered instead of retyping them.
- SMTP is optional. `.env` always carries commented placeholders for it.
- Minimum password length is 6 characters, with no composition rules.
- The admin account is created in the UI on first visit, on the existing setup page.

## 1. Release artifacts

- `scripts/release.sh` builds these targets:
  - `linux/amd64`, `linux/arm64`
  - `darwin/amd64`, `darwin/arm64`
  - `windows/amd64`, `windows/arm64`
- Archive names:
  - `supermetry_<version>_<os>_<arch>.tar.gz`
  - Windows: `.zip` containing `supermetry.exe`
- `SHA256SUMS` lists every archive.
- `.github/workflows/release.yml` triggers on a `v*` tag push (and keeps `workflow_dispatch`). It:
  - creates a GitHub Release with the archives, `SHA256SUMS`, `install.sh` and `install.ps1`;
  - builds and pushes a multi-arch image (`linux/amd64`, `linux/arm64`) to `ghcr.io/velixio/supermetry:<version>` and `:latest`.
- Regular CI stays manual-only (todo #32).

## 2. Download scripts (thin)

`deploy/install.sh` (Linux, macOS) and `deploy/install.ps1` (Windows) do only these steps:

1. Detect the OS and CPU: `uname -s`/`uname -m`, or `$env:PROCESSOR_ARCHITECTURE`.
2. Resolve the version. The default is the latest release (`releases/latest/download/...`). `--version` pins one.
3. Download the archive and `SHA256SUMS`, verify the checksum, and unpack into a temporary directory.
4. Run `supermetry install`, passing through any flags.

Usage:
- `curl -fsSL https://github.com/velixio/supermetry/releases/latest/download/install.sh | sh`
- `irm https://github.com/velixio/supermetry/releases/latest/download/install.ps1 | iex`

When piped, `install.sh` reads answers from `/dev/tty` so the questions still work. Without a TTY it requires `--yes` together with the needed flags.

## 3. `supermetry install` (Go subcommand)

All prompts and setup logic live here: one implementation for every OS, testable, and with no `psql` dependency. It lives in a new `internal/installer` package with small units:

| Unit | Job |
|---|---|
| `prompt` | Asks a question with a default and validation; reads from a TTY or from flags |
| `pgcheck` | Opens a pgx connection with a 5 s timeout and maps errors to plain reasons |
| `envfile` | Renders `.env` from answers; never overwrites without confirmation |
| `proxy` | Renders the nginx or Caddy site config from the domain and listen address |
| `service` | Writes the systemd unit, then runs `daemon-reload`, `enable --now` and `status` (Linux only) |
| `docker` | Renders `docker-compose.yml` and `.env` for the Docker path, then runs `docker compose up -d` |
| `ready` | Polls `GET /readyz` until it returns 200 or 60 s pass |

### 3.1 Flow

1. **Mode.**
   - Linux: Docker or binary. Docker is offered only if `docker compose version` works.
   - macOS and Windows: binary (`.env` only) or Docker, with a note that Docker is better suited to servers.
2. **Public URL**, e.g. `https://analytics.example.com`. It becomes `SUPERMETRY_BASE_URL`. Must be http(s).
3. **Docker path:**
   1. Ask for an install directory (default `/opt/supermetry` on Linux, `./supermetry` elsewhere). Write `docker-compose.yml` there:
      - the image `ghcr.io/velixio/supermetry:<version>`;
      - `postgres:18-alpine` with a named volume and no published port;
      - the app port bound per the listen question (step 4.3).
   2. Write `.env` with:
      - a random `SUPERMETRY_SECRET_KEY` (32 bytes, base64);
      - a random `POSTGRES_PASSWORD`;
      - `SUPERMETRY_BASE_URL`;
      - the SMTP placeholders.
   3. Ask which reverse proxy to set up (nginx, Caddy, none), as in step 4.5.
   4. Run `docker compose up -d`. Poll `/readyz`. On failure, print the last 50 lines of `docker compose logs supermetry`.
4. **Binary path:**
   1. **Database.** Ask for a URL, or host / port (5432) / user / password / database (`supermetry`) / sslmode (`disable` for localhost, `require` otherwise).
   2. **Test before writing anything.** `pgcheck` connects and runs `select current_setting('server_version_num')`.
      - Failures map to a plain reason, for example:
        - host unreachable / connection refused;
        - DNS name not found;
        - password authentication failed for user X;
        - database "X" does not exist;
        - TLS required / not supported;
        - PostgreSQL older than the supported minimum. No minimum is enforced in code today, so the plan pins one after checking which features the migrations use; until then, warn below 18, the version CI runs.
      - Then show the entered values as editable defaults (the password shown masked, Enter keeps it) and ask again. The user can also quit.
      - No file, user or service is created until this test passes.
   3. **Listen address.** Ask: localhost only, `127.0.0.1` (default, recommended behind a reverse proxy), or all interfaces, `0.0.0.0`. The port defaults to `8787` and can be edited. Choosing all interfaces prints a firewall reminder.
   4. **Write `.env`.**
      - Linux service install: `/etc/supermetry/supermetry.env`, mode 0640, owned by `root:supermetry`.
      - Otherwise: `./.env`, mode 0600.
      - Contents:
        - the database settings;
        - a generated secret key;
        - the base URL and listen address;
        - `SUPERMETRY_TRUSTED_PROXIES=127.0.0.1` when a local proxy is chosen;
        - commented SMTP placeholders (`SUPERMETRY_SMTP_HOST`, `_PORT`, `_USER`, `_PASS`, `_FROM`) with a one-line note that they can be filled in any time, followed by a restart.
      - If the file exists, show a diff summary and ask before replacing it. The existing secret key is always kept, because changing it breaks existing logins and encrypted data.
   5. **Reverse proxy (Linux).** Ask: nginx, Caddy or none.
      - **nginx:**
        - write `/etc/nginx/sites-available/supermetry.conf` (or `conf.d/` on distros without `sites-*`) and link it;
        - run `nginx -t`, then reload;
        - if `certbot` is installed, offer `certbot --nginx -d <domain>`; otherwise print that command.
      - **Caddy:** append a site block for the domain to `/etc/caddy/Caddyfile` (backing up the file first), run `caddy validate`, then reload. HTTPS is automatic.
      - **None:** print the address to proxy to.
      - If the proxy binary isn't installed, say so and print its install command. Don't install packages.
   6. **Service (Linux).**
      - Create the `supermetry` system user.
      - Install the binary to `/usr/local/bin/supermetry`.
      - Write `/etc/systemd/system/supermetry.service`, reusing the existing `deploy/supermetry.service`.
      - Run `systemctl daemon-reload` and `systemctl enable --now supermetry`.
   7. **Check.** Poll `/readyz`, then print `systemctl status supermetry --no-pager` (head) and the result. On failure, print the last 50 lines of `journalctl -u supermetry`.
   8. **macOS / Windows.** After the `.env` step, print the command to start the server (`supermetry serve`) and where `.env` lives. No service and no proxy.
5. **Finish.** Print `Open <BASE_URL>/app to create your admin account`, plus the commands to start, stop, restart, enable, disable and view logs.

### 3.2 Non-interactive use

Flags mirror every prompt:
- `--mode docker|binary`
- `--base-url`
- `--database-url` or `--db-host/--db-port/--db-user/--db-password/--db-name/--db-sslmode`
- `--listen`
- `--proxy nginx|caddy|none`
- `--dir`
- `--yes`

With `--yes`, a missing required value is an error, never a guess. A failed database test exits non-zero, because nobody is there to edit the values.

### 3.3 Re-running

The installer is safe to run again:
- it keeps the secret key;
- it asks before replacing `.env` or proxy configs;
- it skips the user or unit if they already exist and match.

Upgrading means running the install script again. That replaces the binary and restarts the service, leaving the config alone.

### 3.4 Privileges

The Linux service install needs root. If not run as root, the installer:
1. asks the questions and tests the database as the current user,
2. then re-executes the writing steps with `sudo`, after saying what it will write.

## 4. Running without configuration

`serve` with no `.env` and no database settings in the environment prints:

```text
Supermetry is not configured. Run `supermetry install`, or set SUPERMETRY_DATABASE_URL, SUPERMETRY_SECRET_KEY and SUPERMETRY_BASE_URL.
```

It exits 1. This replaces the earlier plan of a starter `.env`, which `install` now covers.

## 5. First visit and passwords

- At startup, when the instance has no users, log `No admin account yet: finish setup at <BASE_URL>/app`.
- Minimum password length goes from 10 to 6, with no composition rules:
  - the server-side policy and its message (`internal/platform/http/private.go:574` and the policy check it reports);
  - the `minLength` attributes in `web/src/routes/setup.tsx`, `signup.tsx` and `reset.$token.tsx`;
  - invite acceptance;
  - share-link passwords (`SettingsPages.tsx:489`).
- Check that no special-character rule exists anywhere; remove it if one does.

## 6. Docs touched in this repo

- The README quick start becomes the one-line install command plus "open `/app`".
- `deploy/README.md` gets shorter: installer, manual Docker, manual systemd. The edge cases move to a troubleshooting section.
- The full docs site is separate work in `supermetry-site` (its `todo.md`).

## 7. Testing

- **Unit tests** (`internal/installer`):
  - `.env` rendering, including the SMTP placeholders, the secret key kept on re-run, and file modes;
  - the nginx and Caddy templates (golden files);
  - the compose template;
  - the `pgcheck` error mapping, against a real PostgreSQL with a wrong password, a missing database and a closed port;
  - flag validation with `--yes`;
  - editable defaults after a failed test (scripted prompt input).
- **Password policy:** 5 characters is rejected, 6 is accepted. Update the existing policy tests.
- **Shell:** run `shellcheck` on `install.sh`; test the architecture detection with stubbed `uname`.
- **End to end**, in a disposable Debian 13 container with systemd:
  - the binary path with nginx, then `/readyz`, then creating an admin through the UI (Playwright);
  - the Docker path on a Docker host.
  - Also run a re-run that must change nothing.
- `install.ps1` is checked with a manual run on Windows before the first release; there's no Windows CI.

## Out of scope

- Installing PostgreSQL, nginx, Caddy or Docker packages.
- launchd and Windows services.
- Kubernetes and one-click platforms (Coolify, Dokploy). These may come later as docs pages.
- The docs-site rewrite.
