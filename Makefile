.PHONY: build build-go test lint web web-test release run dev migrate clean distclean kill

# Build everything: the dashboard UI, then the binary with the UI embedded.
build: web
	mkdir -p bin
	go build -tags embedui -o bin/panel ./cmd/panel

# Quick Go-only build (no UI; shows the "UI not built" page). For fast iteration.
build-go:
	mkdir -p bin
	go build -o bin/panel ./cmd/panel

test:
	go test -race ./...

lint:
	go vet ./...
	gofmt -l cmd internal web | (! grep .)

web:
	cd web && npm ci --no-audit --no-fund && npm run build

web-test:
	cd web && npm ci --no-audit --no-fund && npm run typecheck && npm test -- --run

release: build

# Load .env (if present) into the environment of the recipes below.
ENV := set -a; [ -f .env ] && . ./.env; set +a;

# Build the full binary (with the embedded UI) and run it with .env.
run: build
	$(ENV) $(CURDIR)/bin/panel serve

# Run without rebuilding the UI (shows the "UI not built" page if web/dist is absent).
dev: build-go
	$(ENV) $(CURDIR)/bin/panel serve

migrate: build-go
	$(ENV) $(CURDIR)/bin/panel migrate

# Stop the panel server started by make run/dev from this checkout (anchored, so it never matches itself or worktree servers).
kill:
	-pkill -f '^$(CURDIR)/bin/panel serve' && echo "stopped panel" || echo "no panel running"

# Remove build and test artifacts (keeps node_modules and .env).
clean:
	rm -rf bin web/dist web/test-results web/playwright-report web/e2e/screenshots coverage.out

# Also remove installed web dependencies.
distclean: clean
	rm -rf web/node_modules
