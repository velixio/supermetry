.PHONY: build build-go fixtures test lint web web-test release run dev migrate clean distclean kill dev-db-up dev-db-down dev-db-psql

# Build everything: the dashboard UI, then the binary with the UI embedded.
build: web
	mkdir -p bin
	go build -tags embedui -o bin/supermetry ./cmd/supermetry

# Quick Go-only build (no UI; shows the "UI not built" page). For fast iteration.
build-go:
	mkdir -p bin
	go build -o bin/supermetry ./cmd/supermetry

fixtures:
	go run ./tools/gen --tier tiny --seed 42 --out test/golden/testdata/tiny.ndjson --sites test/golden/testdata/tiny.sites.json --truth test/golden/testdata/tiny.truth.json

test: fixtures
	$(TEST_ENV) go test -race ./...

lint:
	go vet ./...
	gofmt -l cmd internal web | (! grep .)

web:
	cd web && bun install --frozen-lockfile && bun run build

web-test:
	cd web && bun install --frozen-lockfile && bun run typecheck && bun run test --run

release: build

# Load .env (if present) into the environment of the recipes below.
ENV := set -a; [ -f .env ] && . ./.env; set +a;

# Development PostgreSQL in Docker on port 5433 (deploy/docker-compose.dev-db.yml).
# When .env does not set SUPERMETRY_DATABASE_URL (or SUPERMETRY_TEST_DSN for tests),
# run/dev/migrate/test start it and use it, so local development needs no settings at all.
DEV_DB := docker compose -f deploy/docker-compose.dev-db.yml
DEV_DB_URL := postgres://supermetry:supermetry@127.0.0.1:5433/supermetry?sslmode=disable
DEV_TEST_DSN := postgres://supermetry:supermetry@127.0.0.1:5433/supermetry_test?sslmode=disable
DEV_DB_START := $(DEV_DB) up -d --wait >/dev/null 2>&1 || { echo "could not start the dev database (is Docker running?)"; exit 1; };
# Also default the two other required settings for local runs: the base URL, and a
# random secret key generated once into .dev/secret-key (gitignored, kept across runs).
DEV_SECRET := .dev/secret-key
DEV_DEFAULTS := if [ -z "$$SUPERMETRY_BASE_URL" ]; then export SUPERMETRY_BASE_URL=http://localhost:8787; fi; if [ -z "$$SUPERMETRY_SECRET_KEY" ]; then mkdir -p .dev; [ -s $(DEV_SECRET) ] || (umask 077; openssl rand -hex 32 > $(DEV_SECRET)); export SUPERMETRY_SECRET_KEY=$$(cat $(DEV_SECRET)); fi;
DB_ENV := $(ENV) $(DEV_DEFAULTS) if [ -z "$$SUPERMETRY_DATABASE_URL" ]; then $(DEV_DB_START) export SUPERMETRY_DATABASE_URL='$(DEV_DB_URL)'; fi;
TEST_ENV := $(ENV) if [ -z "$$SUPERMETRY_TEST_DSN" ]; then $(DEV_DB_START) export SUPERMETRY_TEST_DSN='$(DEV_TEST_DSN)'; fi;

# Build the full binary (with the embedded UI) and run it with .env.
run: build
	$(DB_ENV) $(CURDIR)/bin/supermetry serve

# Build the dashboard once, the first time it is needed; later runs reuse web/dist
# (run `make web` after changing the dashboard).
web/dist/index.html:
	$(MAKE) web

# Run with the dashboard, without rebuilding it on every start.
dev: web/dist/index.html
	mkdir -p bin
	go build -tags embedui -o bin/supermetry ./cmd/supermetry
	$(DB_ENV) $(CURDIR)/bin/supermetry serve

migrate: build-go
	$(DB_ENV) $(CURDIR)/bin/supermetry migrate

# Stop the supermetry server started by make run/dev from this checkout (anchored, so it never matches itself or worktree servers).
kill:
	-pkill -f '^$(CURDIR)/bin/supermetry serve' && echo "stopped supermetry" || echo "no supermetry running"

# The dev database also starts on its own with run/dev/migrate/test; these are for manual use.
dev-db-up:
	$(DEV_DB) up -d --wait

# Stop it; the data stays in the supermetry-dev-pg volume.
dev-db-down:
	$(DEV_DB) down

dev-db-psql:
	$(DEV_DB) exec postgres psql -U supermetry -d supermetry

# Remove build and test artifacts (keeps node_modules and .env).
clean:
	rm -rf bin web/dist web/test-results web/playwright-report web/e2e/screenshots coverage.out

# Also remove installed web dependencies.
distclean: clean
	rm -rf web/node_modules
