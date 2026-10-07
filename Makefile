.PHONY: build test lint

build:
	mkdir -p bin
	go build -o bin/panel ./cmd/panel

test:
	go test -race ./...

lint:
	go vet ./...
	gofmt -l cmd internal | (! grep .)
