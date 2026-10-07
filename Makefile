.PHONY: build test lint web web-test release

build:
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

release: web
	mkdir -p bin
	go build -tags embedui -o bin/panel ./cmd/panel
