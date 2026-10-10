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
	go test -race ./...

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

# Build the full binary (with the embedded UI) and run it with .env.
run: build
	$(ENV) $(CURDIR)/bin/supermetry serve

# Run without rebuilding the UI (shows the "UI not built" page if web/dist is absent).
dev: build-go
	$(ENV) $(CURDIR)/bin/supermetry serve

migrate: build-go
	$(ENV) $(CURDIR)/bin/supermetry migrate

# Stop the supermetry server started by make run/dev from this checkout (anchored, so it never matches itself or worktree servers).
kill:
	-pkill -f '^$(CURDIR)/bin/supermetry serve' && echo "stopped supermetry" || echo "no supermetry running"

# Development PostgreSQL in Docker on port 5433 (deploy/docker-compose.dev-db.yml).
DEV_DB := docker compose -f deploy/docker-compose.dev-db.yml

dev-db-up:
	$(DEV_DB) up -d --wait
	@echo "app:   SUPERMETRY_DATABASE_URL=postgres://supermetry:supermetry@127.0.0.1:5433/supermetry?sslmode=disable"
	@echo "tests: SUPERMETRY_TEST_DSN=postgres://supermetry:supermetry@127.0.0.1:5433/supermetry_test?sslmode=disable"

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
