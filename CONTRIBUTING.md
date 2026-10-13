# Contributing

Thanks for helping. Please open an issue before starting large changes so we can agree on the approach. Everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md).

## Setup

- Go (see `go.mod` for the version), [Bun](https://bun.sh) for the dashboard, and Docker.
- No database setup: `make dev` and `make test` start PostgreSQL in Docker on port 5433 and use it. To use your own server instead, set `SUPERMETRY_DATABASE_URL` (app) or `SUPERMETRY_TEST_DSN` (tests, a disposable database; tests create their own temporary schemas).

`make dev` is the watch mode: open <http://localhost:8787>, the Go server, exactly as in production (it listens on 127.0.0.1 only). It reverse-proxies the UI to a Vite dev server on :5173, so hot reload works and login, CSRF and cookies behave as usual. The server is rebuilt and restarted when Go files, `go.mod`/`go.sum` or embedded assets under `cmd/` and `internal/` (and `web/*.go`) change; a compile error is printed and the old server keeps running. Ctrl+C (or `make kill`) stops everything. `make run` serves the embedded production-like build on :8787, and `make dev-embedded` builds the UI once and runs it without watching. `make test-dev` smoke-tests the watcher script.

```sh
make build
make test
make lint
cd web && bun install --frozen-lockfile && bun run typecheck && bun run test --run
```

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
- Write commit messages that describe the change only.

## License

Supermetry is licensed under [Apache-2.0](LICENSE). You keep the copyright in your work. As section 5 of the license says, anything you submit in a pull request is contributed under Apache-2.0; there is no separate agreement to sign.

If an employer or another organization owns your work, make sure it allows you to contribute it under Apache-2.0. Do not submit code you do not have the right to contribute, or code copied from a source whose license does not allow it. Do not copy code from other analytics projects unless its license allows it and you say where it came from.
