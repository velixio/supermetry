#!/usr/bin/env bash
# Regenerates internal/sourcemap/testdata/vite/. Builds in /tmp so no machine
# path reaches the source map; nothing is uploaded. Run under the heavy lock:
#   flock /tmp/claude-1000/supermetry-heavy.lock tools/sentry-fixtures/sourcemaps/gen.sh
# The debug id is random per build, so regenerating changes every fixture file
# (and the README line numbers must still hold).
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
out=$here/../../../internal/sourcemap/testdata/vite
work=/tmp/sm-fixture-app
rm -rf "$work" && mkdir -p "$work"
cp -r "$here"/{package.json,bun.lock,vite.config.mjs,index.html,src,gen.mjs} "$work"/
cd "$work"
bun install --frozen-lockfile
bun run build
bun gen.mjs "$(cd "$out" 2>/dev/null && pwd || (mkdir -p "$out" && cd "$out" && pwd))"
grep -rlE '/home/|/Users/' "$out" && { echo "machine path leaked" >&2; exit 1; } || true
rm -rf "$work"
