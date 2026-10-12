# Easy Install Implementation Plan

> **For agentic workers:** each task below is one worker job in its own worktree (the job runner script). Use superpowers test-driven-development and verification-before-completion. Steps use checkbox (`- [ ]`) syntax.

**Goal:** a one-command installer that downloads the right release binary, asks a few questions, tests PostgreSQL before writing anything, and leaves a running Supermetry (Docker or systemd) with an admin-setup link.

**Architecture:** the shell and PowerShell scripts only download the binary and verify it. All prompts and setup live in a new Go package, `internal/installer`, which the `supermetry install` subcommand runs. Releases are built for 6 OS/arch targets, and the image is published to GHCR.

**Tech Stack:** Go (stdlib, pgx v5, `embed`, `text/template`), POSIX sh, PowerShell 5+, GitHub Actions, Docker Compose.

**Spec:** `docs/superpowers/specs/2026-10-11-easy-install-design.md`. Read it first; section numbers below refer to it.

## Global Constraints

- Release targets: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64, windows/arm64. Windows ships as a `.zip` with `supermetry.exe`; the rest as `.tar.gz`. Archive name: `supermetry_<version>_<os>_<arch>.<ext>`.
- Image: `ghcr.io/velixio/supermetry:<version>` and `:latest`, multi-arch amd64 and arm64. Postgres image: `postgres:18-alpine`.
- The server starts with plain `supermetry` (no `serve`); `serve` stays as an alias.
- Defaults: reverse proxy `none`; Docker image `download`; listen `127.0.0.1:8787`; nginx file `/etc/nginx/conf.d/supermetry.conf`.
- Nothing is written to disk, and no user or service is created, before the PostgreSQL test passes (binary path).
- A failed database test re-asks with the entered values as editable defaults (password masked, Enter keeps it).
- `.env` always contains commented SMTP placeholders: `SUPERMETRY_SMTP_HOST`, `SUPERMETRY_SMTP_PORT`, `SUPERMETRY_SMTP_USER`, `SUPERMETRY_SMTP_PASS`, `SUPERMETRY_SMTP_FROM`.
- An existing `SUPERMETRY_SECRET_KEY` is never replaced. The secret key is 32 random bytes, std base64.
- The minimum password length is 6, with no composition rules.
- No package installs (PostgreSQL, nginx, Caddy, Docker). No launchd or Windows service.
- License: Apache-2.0. Never copy code from the AGPL/EUPL reference repos. No AI or assistant names in code, comments or commit messages.

## Review Focus

1. A piped `curl | sh` with no TTY and no `--yes` must stop with a clear message, not hang or take defaults silently. (Task 1: shell test with stdin closed.)
2. A database password containing `@`, `:`, `/`, `#`, `%` or a space must round-trip into a working URL, and into `.env` without breaking parsing. (Task 2: `TestBuildDatabaseURLEscapes`, `TestEnvRoundTripSpecialChars` using `config.LoadDotEnv`.)
3. Re-running the installer over an existing `.env` keeps the secret key and changes nothing without confirmation. (Task 2: `TestRenderEnvKeepsSecret`.)
4. A non-root Linux run must not leave half-written files when `sudo` is refused. (Task 2: the writer returns a plan; the test asserts no write happens before confirmation.)
5. A 5-character password is rejected and a 6-character one accepted on every path: setup, signup, reset, invite, change password. (Task 3.)

---

### Task 1: Release targets, download scripts, systemd unit (job `install-release`)

**Files:**
- Modify: `scripts/release.sh`. Default targets are all 6; `.zip` for Windows (binary `supermetry.exe`); `SHA256SUMS` covers every archive. Also copy `deploy/install.sh` and `deploy/install.ps1` into `dist/`.
- Modify: `.github/workflows/release.yml`:
  - trigger on `push: tags: ['v*']` plus `workflow_dispatch`;
  - `permissions: contents: write, packages: write`;
  - after the build, create a GitHub Release with `gh release create "$GITHUB_REF_NAME" dist/*` (only on a tag);
  - a second job uses docker/setup-qemu-action, docker/setup-buildx-action and docker/login-action (ghcr, GITHUB_TOKEN), then docker/build-push-action with `platforms: linux/amd64,linux/arm64` and tags `ghcr.io/velixio/supermetry:${version}` and `:latest`. Pin actions to major versions as the file already does.
- Rewrite: `deploy/install.sh`. POSIX sh, thin (spec §2):
  - detect OS (Linux, Darwin) and arch (x86_64/amd64 → amd64, aarch64/arm64 → arm64);
  - version: `--version` / `SUPERMETRY_VERSION`, else `latest`. Base URL `https://github.com/velixio/supermetry/releases/latest/download` or `.../releases/download/v<version>`. For `latest`, resolve the version from the redirect of `https://github.com/velixio/supermetry/releases/latest` (`curl -fsSLI -o /dev/null -w '%{url_effective}'`). `SUPERMETRY_RELEASE_URL` overrides the base;
  - download the archive and `SHA256SUMS` with curl or wget; verify with `sha256sum` or `shasum -a 256`;
  - unpack into a mktemp dir, then `exec "$tmp/supermetry" install "$@"`;
  - when stdin is not a TTY but `/dev/tty` is readable, run with `</dev/tty`. When neither is available and `--yes` is not among the args, exit 1 with `install.sh: no terminal for questions; pass --yes with the settings as flags (see supermetry install --help)`.
- Create: `deploy/install.ps1`. The same flow for Windows: `$env:PROCESSOR_ARCHITECTURE` (AMD64 → amd64, ARM64 → arm64), `Invoke-WebRequest`, `Get-FileHash -Algorithm SHA256` against `SHA256SUMS`, `Expand-Archive`, then run `supermetry.exe install @args`.
- Modify: `deploy/supermetry.service`. Change `ExecStart=/usr/local/bin/supermetry`, dropping `serve`.
- Test: `scripts/test-install-sh.sh` (new). Use stubbed `uname`, `curl` and `sha256sum` on PATH to check:
  - arch mapping for 4 inputs;
  - an unsupported arch exits non-zero;
  - a checksum mismatch exits non-zero;
  - no TTY and no `--yes` exits 1 with the message above.
  Run `shellcheck deploy/install.sh scripts/test-install-sh.sh` if shellcheck is installed.

- [ ] Write `scripts/test-install-sh.sh` with the 4 cases; run it; expect failures.
- [ ] Rewrite `deploy/install.sh`; run the test until it passes; run shellcheck.
- [ ] Write `deploy/install.ps1`.
- [ ] Update `scripts/release.sh`; run `SUPERMETRY_RELEASE_TARGETS="linux/amd64 windows/amd64" scripts/release.sh` (or the closest local equivalent) and check that `dist/` has a `.tar.gz`, a `.zip`, `SHA256SUMS` and both install scripts.
- [ ] Update `release.yml` and `supermetry.service`.
- [ ] Write the commit plan message to the commit message file for `install-release`.

### Task 2: `internal/installer` and `supermetry install` (job `install-cmd`)

**Files (create unless noted):**
- `internal/installer/prompt.go`: a `Prompter` struct (in `io.Reader`, out `io.Writer`). Methods:
  - `Ask(label, def string, validate func(string) error) (string, error)`;
  - `AskSecret(label, def string)`, which shows `[keep]` when `def` is non-empty, reads without echo when the input is a terminal (use `golang.org/x/term` if it is already in go.mod, else plain read), and returns `def` on empty input;
  - `Choose(label string, options []string, def string) (string, error)`;
  - `Confirm(label string, def bool) (bool, error)`.
- `internal/installer/dbconfig.go`: `type DBConfig struct{ URL, Host, Port, User, Password, Name, SSLMode string }` with `func (c DBConfig) ConnString() string`. When `URL` is set it is returned unchanged; otherwise build `postgres://user:pass@host:port/name?sslmode=` with `url.UserPassword` escaping. The default sslmode is `disable` for localhost/127.0.0.1/::1, `require` otherwise.
- `internal/installer/pgcheck.go`: `func CheckPostgres(ctx context.Context, conn string) (version int, err error)`. Uses a pgx connect with a 5 s timeout and `select current_setting('server_version_num')::int`. Errors map to `*CheckError{Reason string, Field string}` via `pgconn.PgError` codes (28P01 → "password authentication failed for user X", field `password`; 3D000 → "database X does not exist", field `name`; 28000 → field `user`), plus `net.OpError`/DNS errors (field `host`) and TLS errors (field `sslmode`). Version < 180000 gives a warning, not an error.
- `internal/installer/envfile.go`: `type EnvValues struct{ DatabaseURL, BaseURL, Listen, TrustedProxies, SecretKey string; Docker bool; PostgresPassword string }`, plus:
  - `func NewSecretKey() (string, error)`;
  - `func RenderEnv(v EnvValues) []byte`, which always appends the commented SMTP block with the note `# Optional. Fill in any time, then restart Supermetry.`;
  - `func ReadExistingSecret(path string) string`, which reuses `config.LoadDotEnv`-compatible parsing.
- `internal/installer/proxy.go` plus `templates/nginx.conf.tmpl` and `templates/caddy.tmpl`: `func RenderNginx(domain, upstream string) []byte` and `func RenderCaddy(domain, upstream string) []byte`. nginx proxies `/` with `Host`, `X-Forwarded-For`, `X-Forwarded-Proto` and `X-Real-IP`, `client_max_body_size 10m`, and listens on 80 (certbot adds TLS). Write the templates from scratch; do not copy from `knowledge/`.
- `internal/installer/compose.go` plus `templates/docker-compose.yml.tmpl`: `func RenderCompose(c ComposeValues) []byte`, where `ComposeValues{Version, Image string /* "download"|"build" */, ListenHost, Port string}`. Services:
  - `supermetry`: `image:` GHCR, or `build: ./src`; `env_file: .env`; `SUPERMETRY_DATABASE_HOST=postgres`; ports `ListenHost:Port:8787`; volume `supermetry-data:/var/lib/supermetry`; `depends_on` postgres healthy;
  - `postgres`: `postgres:18-alpine`; `POSTGRES_USER`/`POSTGRES_DB` `supermetry`; password from `.env`; volume at `/var/lib/postgresql`; `pg_isready` healthcheck; no ports.
  Check how `internal/config` builds the URL from `SUPERMETRY_DATABASE_HOST` and the user/password settings, and use those exact env names.
- `internal/installer/service.go` plus `templates/supermetry.service`: a copy of `deploy/supermetry.service` with `ExecStart=/usr/local/bin/supermetry`. Steps run through a `Runner interface{ Run(ctx, name string, args ...string) ([]byte, error) }` so tests use a fake. Ensure the user exists, install the binary (copy `os.Executable()` to `/usr/local/bin/supermetry`), write the unit, run `systemctl daemon-reload` and `systemctl enable --now supermetry`.
- `internal/installer/ready.go`: `func WaitReady(ctx, baseURL string, timeout time.Duration) error`, polling `GET <listen>/readyz` every second.
- `internal/installer/install.go`: `type Options` (every flag in spec §3.2: Mode, BaseURL, DB DBConfig, Listen, Proxy, Image, Dir, Yes, Version) and `func Run(ctx, opts Options, p *Prompter, sys System) error`, implementing spec §3.1. `System` bundles `Runner`, a file writer `WriteFile(path string, data []byte, mode os.FileMode) error`, `GOOS`, `IsRoot`, `LookPath`, and a `Check func(ctx, conn) (int, error)` hook for tests. Writes happen only after the DB test passes. On a failed check, print `Reason`, then re-ask every DB field with the previous values as defaults. With `Yes`, return the error instead. If not root on Linux service install, collect all writes into a plan, print it, and re-exec `sudo <exe> install --apply <tempfile>` (or print the exact sudo command if sudo is missing).
- Modify `cmd/supermetry/main.go`: add `case "install"`, which parses flags with the `flag` package into `installer.Options` and calls `installer.Run`; add `install` to `usage`. Also, when the command is `serve` (the default) and no `.env` exists and neither `SUPERMETRY_DATABASE_URL` nor `SUPERMETRY_DATABASE_HOST` is set, print `Supermetry is not configured. Run "supermetry install", or set SUPERMETRY_DATABASE_URL, SUPERMETRY_SECRET_KEY and SUPERMETRY_BASE_URL.` and exit 1. Update any usage text that says `supermetry serve` to plain `supermetry`.
- Tests in `internal/installer/*_test.go`:
  - `TestBuildDatabaseURLEscapes`: a password `p@ss:w/rd#% x` round-trips through `url.Parse`;
  - `TestEnvRoundTripSpecialChars`: render, write to a temp file, `config.LoadDotEnv`, and the values are equal;
  - `TestRenderEnvHasSMTPPlaceholders`;
  - `TestRenderEnvKeepsSecret`;
  - golden tests for nginx, Caddy and compose (`download` and `build`);
  - `TestRunRetriesDBWithDefaults`: a scripted Prompter input where the first check fails with field `password`, the second passes; assert the second prompt defaults equal the first answers and that no WriteFile happens before the passing check;
  - `TestRunYesMissingValueErrors`;
  - `TestRunYesFailedDBExits`;
  - `TestCheckPostgresReasons` against `SUPERMETRY_TEST_DSN` (skip if unset) with a wrong password, a missing database and a closed port (127.0.0.1:1);
  - `TestRunNoWritesBeforeConfirm` for the non-root plan path.

- [ ] Write the failing tests for dbconfig, envfile, proxy and compose; implement; pass.
- [ ] Write the failing pgcheck tests; implement; pass.
- [ ] Write the failing Run flow tests with fakes; implement `install.go`; pass.
- [ ] Wire `cmd/supermetry/main.go`; run `go build ./... && ./supermetry install --help` (or `go run ./cmd/supermetry install --help`).
- [ ] gofmt, `go vet ./...`, `go test ./...`; write the commit message file for `install-cmd`.

### Task 3: Password minimum 6 and the admin-setup log line (job `install-auth`)

**Files:**
- Modify: the password policy check behind `writePasswordPolicyProblem` (`internal/platform/http/private.go:571-575`; find the policy function under `internal/platform/auth`). Minimum 6, no composition rules; message `password must be at least 6 characters`. Grep the whole repo for any other length or character-class checks (`10`, `unicode.Is`, special characters) on passwords, including share-link passwords, and align them.
- Modify: `web/src/routes/setup.tsx`, `signup.tsx` and `reset.$token.tsx`, plus `web/src/features/settings/SettingsPages.tsx` (share password), any invite-accept and change-password forms (grep `minLength={10}`), and any UI copy saying "10 characters". Set `minLength={6}`.
- Modify: `cmd/supermetry/serve.go`. After migrations, if the users table is empty, log with slog at Info: `msg="No admin account yet: finish setup at <BASE_URL>/app"`. Find the existing user-count query used by the setup handler and reuse it.
- Tests: update the existing policy tests. Add cases: 5 characters rejected and 6 accepted, for setup, signup, reset, invite accept and change password (table test at the handler level where tests already exist). Update web tests that assert 10.

- [ ] Change the tests to expect 6; run them; they fail.
- [ ] Change the policy and forms; Go and web gates pass.
- [ ] Add the log line with a test if serve has a testable seam; otherwise check it manually against the dev database.
- [ ] Write the commit message file for `install-auth`.

### Task 4: README and deploy docs (job `install-docs`)

**Files:**
- Modify: `README.md` Quick start becomes:
  - Linux/macOS: `curl -fsSL https://github.com/velixio/supermetry/releases/latest/download/install.sh | sh`
  - Windows: `irm https://github.com/velixio/supermetry/releases/latest/download/install.ps1 | iex`
  - then "Open `<your URL>/app` and create your admin account".
  Keep "Build from source" (`make build`, run `./bin/supermetry`).
- Rewrite: `deploy/README.md`, in short task sections:
  1. Install with the installer: what it asks (mode, URL, PostgreSQL, listen address, reverse proxy, image source) and the unattended flags from spec §3.2.
  2. Docker Compose by hand.
  3. Binary and systemd by hand (`ExecStart=/usr/local/bin/supermetry`).
  4. Manage the service: start, stop, restart, enable, disable, status, `journalctl -u supermetry -f`.
  5. Email (SMTP), optional: edit `.env`, restart.
  6. Upgrade: rerun the installer.
  7. Backup and restore: keep the existing content.
  8. Reverse proxy: nginx at `/etc/nginx/conf.d/supermetry.conf`, Caddy.
  9. Session replay: keep the existing content.
  10. Troubleshooting: move the buildx, old `deploy-*` volume names and PG 18 volume path paragraphs here.
  Every command must be copy-pasteable. Drop `serve` everywhere.
- Modify: `.env.example`. Make sure the SMTP keys are present as commented placeholders.

- [ ] Rewrite the docs; check every env var name you mention exists via `grep -rn NAME internal/config`.
- [ ] Write the commit message file for `install-docs`.

## Integration (orchestrator)

- [ ] After each job: review the diff, run the gates in the worktree, commit with the job's message, and merge into main. Merge order: auth, release, cmd, docs.
- [ ] Final: end-to-end on a Debian 13 systemd container (spec §7). Update todo.md.
