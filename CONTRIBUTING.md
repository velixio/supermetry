# Contributing

Thanks for helping. Please open an issue before starting large changes so we can agree on the approach.

## Setup

- Go (see `go.mod` for the version), [Bun](https://bun.sh) for the dashboard, and Docker.
- No database setup: `make dev` and `make test` start PostgreSQL in Docker on port 5433 and use it. To use your own server instead, set `SUPERMETRY_DATABASE_URL` (app) or `SUPERMETRY_TEST_DSN` (tests, a disposable database; tests create their own temporary schemas).

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

By contributing you agree that your work is licensed under Apache-2.0, the same as the project.
