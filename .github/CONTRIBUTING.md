# Contributing

Thanks for helping. Please open an issue before starting large changes so we can agree on the approach. Everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md).

## Setup

- Go (see `go.mod` for the version), [Bun](https://bun.sh) for the dashboard, and Docker.
- No database setup: `make dev` and `make test` start PostgreSQL in Docker on port 5433 and use it. To use your own server instead, set `SUPERMETRY_DATABASE_URL` (app) or `SUPERMETRY_TEST_DSN` (tests, a disposable database; tests create their own temporary schemas).

`make dev` is the watch mode: open <http://localhost:8787>, the Go server, exactly as in production (it listens on 127.0.0.1 only). It reverse-proxies the UI to a Vite dev server on :5173, so hot reload works and login, CSRF and cookies behave as usual. The server is rebuilt and restarted when Go files, `go.mod`/`go.sum` or embedded assets under `cmd/` and `internal/` (and `web/*.go`) change; a compile error is printed and the old server keeps running. Ctrl+C (or `make kill`) stops everything. `make run` serves the embedded production-like build on :8787, and `make dev-embedded` builds the UI once and runs it without watching. `make test-dev` smoke-tests the watcher script.

Behind a domain or reverse proxy, set `SUPERMETRY_BASE_URL=https://your.domain` in `.env`: its hostname is allowed for the Vite dev server automatically (the banner shows it), and `make dev` prints that URL to open. Use `SUPERMETRY_DEV_ALLOWED_HOSTS=a.supermetry.com,.example.com` for extra names (`.example.com` covers subdomains; `true` and `*` are ignored, the host check is never disabled). The proxy must forward the `Host` header and WebSocket upgrades (hot reload). `make dev` listens on 127.0.0.1 unless `SUPERMETRY_LISTEN` is set, so a proxy on another machine needs `SUPERMETRY_LISTEN` set explicitly.

```sh
make build      # dashboard (bun install --frozen-lockfile, bun run build), then bin/supermetry
make test       # go test -race ./... (starts the dev PostgreSQL if no test DSN is set)
make lint       # go vet and gofmt
make web-test   # web typecheck and vitest
```

### Tests

- **Go:** `SUPERMETRY_TEST_DSN=postgres://user:pass@127.0.0.1:5432/postgres go test ./internal/query/...` runs one package against your own PostgreSQL. Without the variable, database tests skip. `make test-fast` skips the two slowest database packages and runs the web suite too.
- **Web:** `cd web && bun install --frozen-lockfile && bunx vitest run` runs all unit tests. Pass a file path to run one.
- **End to end:** `make test-e2e` builds the binary and runs the Playwright specs.

## Repository layout

```text
cmd/supermetry/   the binary: serve, migrate, backup, restore and other commands
internal/         all server code, one package per concern (ingest, session, query,
                  api, auth, sites, imports, app* for App Monitoring, db/migrations, ...)
web/              the dashboard: Vite, React, TypeScript (src/features, src/routes)
sdk/              client libraries and framework integrations
deploy/           Dockerfile, compose files, installer, systemd unit, Helm chart
docs/             user and API docs; docs/features.md lists what ships
tools/, scripts/  test data generators, benchmarks and release helpers
```

Migrations are numbered SQL files in `internal/db/migrations`. Add a new one; never edit a released one.

## Rules of the project

- Single Go binary plus PostgreSQL. Redis stays optional. No ClickHouse or similar stores, Kafka, separate workers or Node in production.
- No raw IPs or full User-Agents stored. Keep that true in logs too.
- Never do a database read on the ingest hot path.
- Migrations are forward-only and transactional.
- Metric changes need a test against hand-computed fixtures.
- Use Bun, not npm.

## Pull requests

- Keep them focused, with commits that build and pass `go vet`.
- Add or update tests, and describe how you verified the change.
- Write commit messages that describe the change only: a short imperative summary line, then a body if the reason is not obvious. Several small commits are better than one large one.
- Fill in the pull request template. Include screenshots for UI changes.

## Good first issues

Look for the `good first issue` label. Good places to start:

- Fix or extend a document in `docs/`.
- Add a test for an existing metric, using a small hand-computed fixture.
- Add a missing install recipe, referrer entry or translation string.
- Improve an error message or an empty state in the dashboard.

Comment on the issue before you start, so two people do not do the same work. Ask questions in [Discussions](https://github.com/velixio/supermetry/discussions).

Report security problems privately, as described in [SECURITY.md](SECURITY.md).

## License

Supermetry is licensed under [Apache-2.0](../LICENSE). You keep the copyright in your work. As section 5 of the license says, anything you submit in a pull request is contributed under Apache-2.0; there is no separate agreement to sign.

If an employer or another organization owns your work, make sure it allows you to contribute it under Apache-2.0. Do not submit code you do not have the right to contribute, or code copied from a source whose license does not allow it. Do not copy code from other analytics projects unless its license allows it and you say where it came from.
