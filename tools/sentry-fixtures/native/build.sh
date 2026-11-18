#!/usr/bin/env bash
# Regenerates the committed native fixtures in internal/debugfile/testdata.
# Needs Go and gcc (binutils addr2line for the oracle). Run from anywhere.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
out="$here/../../../internal/debugfile/testdata"
mkdir -p "$out"
cd "$here"
export CGO_ENABLED=0
GOOS=linux GOARCH=arm64 go build -trimpath -ldflags=-B=gobuildid -o "$out/fix-linux-arm64" fixture.go
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags=-B=gobuildid -o "$out/fix-linux-amd64" fixture.go
GOOS=darwin GOARCH=arm64 go build -trimpath -o "$out/fix-darwin-arm64" fixture.go
GOOS=darwin GOARCH=amd64 go build -trimpath -o "$out/fix-darwin-amd64" fixture.go
gcc -g -O1 -shared -fPIC -fdebug-prefix-map="$here"=. -Wl,--build-id fixture.c -o "$out/libfix.so"
go run expected.go "$out"
