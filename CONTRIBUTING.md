# Contributing

Thanks for helping. Please open an issue before starting large changes so we can agree on the approach.

## Setup

- Go (see `go.mod` for the version), PostgreSQL, and [Bun](https://bun.sh) for the dashboard.
- Tests that touch the database use `SUPERMETRY_TEST_DSN`. Point it at a disposable PostgreSQL database; tests create their own temporary schemas.

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
