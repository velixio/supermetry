.PHONY: build build-go fixtures test test-fast test-all test-e2e lint web web-test release run dev dev-embedded migrate clean distclean kill dev-db-up dev-db-down dev-db-psql soak accuracy-ab accuracy-live api-inventory bench bench-full bench-compare

# Build everything: the dashboard UI, then the binary with the UI embedded.
build: web
	mkdir -p bin
	go build -tags embedui -o bin/supermetry ./cmd/supermetry

# Quick Go-only build (no UI; shows the "UI not built" page). For fast iteration.
build-go:
	mkdir -p bin
	go build -o bin/supermetry ./cmd/supermetry

accuracy-ab: build-go
	python3 -m unittest tools.bench.accuracy.ab.test_live_scenarios
	go test ./internal/session ./internal/realtime -run 'Test(ActiveVisitorsSince|RichSnapshotLiveCount)' -count=1
	python3 tools/bench/accuracy/ab/run.py

ACCURACY_CREDS ?= /tmp/claude-1000/-home-user-code-supermetry/a367a7b0-447b-4879-9004-f5690263327e/scratchpad/cmp-creds.env
accuracy-live:
	python3 tools/bench/accuracy/compare_live.py --live --creds "$(ACCURACY_CREDS)"

fixtures:
	go run ./tools/gen --tier tiny --seed 42 --out test/golden/testdata/tiny.ndjson --sites test/golden/testdata/tiny.sites.json --truth test/golden/testdata/tiny.truth.json

test: fixtures
	$(TEST_ENV) go test -race ./...

api-inventory:
	@set -e; hits=$$(mktemp); trap 'rm -f "$$hits"' EXIT; \
		export SUPERMETRY_ROUTE_HITS="$$hits"; \
		$(TEST_ENV) go test ./... -skip '^TestBackupRestoreIntegration$$'; \
		go run ./tools/apiinv -hits "$$hits" -out docs/api

bench:
	scripts/api-bench.sh quick

bench-full:
	scripts/api-bench.sh full

bench-compare:
	@test -n "$(BENCH_BASELINE)" || { echo "set BENCH_BASELINE to a result.json file" >&2; exit 2; }
	scripts/api-bench.sh compare "$(BENCH_BASELINE)" --tolerance "$(or $(BENCH_TOLERANCE),0.25)"

# Fast feedback excludes the two database-heavy packages and runs unit-focused
# Go tests plus the complete browser-independent web suite.
test-fast:
	$(TEST_ENV) GOMAXPROCS=6 go test -short -p 3 -timeout 25m $$(go list ./... | grep -Ev '/internal/(query|ingest)$$')
	cd web && bun install --frozen-lockfile && bun run test --run --maxWorkers=3

test-e2e: build
	go run ./tools/gen --tier tiny --seed 42 --out test/golden/testdata/tiny.ndjson --sites test/golden/testdata/tiny.sites.json --truth test/golden/testdata/tiny.truth.json
	cd web && bunx playwright test e2e/account-navigation.spec.ts e2e/authorization.spec.ts --reporter=list --workers=1
	cd web && bunx playwright test e2e/not-found.spec.ts e2e/privacy-navigation.spec.ts --reporter=list --workers=1
	cd web && bunx playwright test e2e/a11y-audit.spec.ts e2e/api-docs.spec.ts e2e/dashboard.spec.ts e2e/dashboard-where-when.spec.ts e2e/navigation.spec.ts e2e/privacy.spec.ts e2e/revenue.spec.ts e2e/sessions.spec.ts e2e/settings.spec.ts e2e/vitals.spec.ts --reporter=list --workers=1
	cd web && bunx playwright test e2e/account-sessions.spec.ts e2e/accuracy-live.spec.ts e2e/notifications.spec.ts e2e/project-currency.spec.ts e2e/replay-fit.spec.ts e2e/share.spec.ts --reporter=list --workers=1

test-all: fixtures
	go vet ./...
	$(TEST_ENV) GOMAXPROCS=6 go test -p 3 -timeout 25m ./...
	cd web && bun install --frozen-lockfile
	cd web && bun run typecheck
	cd web && bun run test --run --maxWorkers=3
	cd web && bun run build
	$(MAKE) test-e2e

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


# Rebuild the dashboard when any source input changes.
web/dist/index.html: $(shell find web/src web/packages web/public -type f 2>/dev/null) web/package.json web/index.html web/vite.config.ts
	$(MAKE) web

# make dev: watch mode. Open http://localhost:5173 (the Vite dev server with hot reload).
# Vite proxies the API, tracker, health and short-link paths to the Go server on :8787.
# The Go server is rebuilt and restarted when .go files, go.mod/go.sum or the assets
# embedded from cmd/ and internal/ change; a compile error keeps the old server running.
# UI edits need no restart. SUPERMETRY_BASE_URL defaults to http://localhost:5173 here
# (the origin the browser uses); if .env sets another value, login through :5173 fails
# the Origin check, so leave it unset for make dev. Ctrl+C stops both processes.
dev:
	$(ENV) if [ -z "$$SUPERMETRY_BASE_URL" ]; then export SUPERMETRY_BASE_URL=http://localhost:5173; fi; \
		$(DB_ENV) exec sh $(CURDIR)/scripts/dev.sh

# One-shot embedded build served by Go on :8787 (the old make dev). No watching.
dev-embedded: web/dist/index.html
	mkdir -p bin
	go build -tags embedui -o bin/supermetry ./cmd/supermetry
	$(DB_ENV) $(CURDIR)/bin/supermetry serve

migrate: build-go
	$(DB_ENV) $(CURDIR)/bin/supermetry migrate

# Stop the supermetry server started by make run/dev from this checkout (anchored, so it never matches itself or worktree servers).
# Also stops the make dev supervisor and its Vite dev server from this checkout.
kill:
	-pkill -f '^sh $(CURDIR)/scripts/dev.sh' && echo "stopped dev watcher" || true
	-pkill -f '^[^ ]*(node|bun) .*$(CURDIR)/web/node_modules/\.bin/vite' && echo "stopped vite" || true
	-pkill -f '^$(CURDIR)/bin/supermetry serve' && echo "stopped supermetry" || echo "no supermetry running"

# The dev database also starts on its own with run/dev/migrate/test; these are for manual use.
dev-db-up:
	$(DEV_DB) up -d --wait

# Stop it; the data stays in the supermetry-dev-pg volume.
dev-db-down:
	$(DEV_DB) down

dev-db-psql:
	$(DEV_DB) exec postgres psql -U supermetry -d supermetry

soak:
	python3 tools/bench/soak/soak.py $(SOAK_ARGS)

# Remove build and test artifacts (keeps node_modules and .env).
clean:
	rm -rf bin web/dist web/test-results web/playwright-report web/e2e/screenshots coverage.out

# Also remove installed web dependencies.
distclean: clean
	rm -rf web/node_modules
