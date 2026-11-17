#!/usr/bin/env bash
# Usage: record-case.sh <sdk> <case> <item-type> <notes> -- <generator command...>
# Starts the capture server on a free port, runs the generator with
# SENTRY_FIXTURE_DSN set, then keeps the first captured envelope whose first
# item type is <item-type> as internal/sentryingest/testdata/<sdk>/<case>.*
# The generator command's exit status is ignored (crash cases exit non-zero).
# Set CAPTURE_PICK=<n> to keep the n-th match instead of the first.
set -uo pipefail
sdk="$1"; case_="$2"; type_="$3"; notes="$4"; shift 4
[ "$1" = "--" ] && shift
root="$(cd "$(dirname "$0")/../.." && pwd)"
out="$root/internal/sentryingest/testdata/$sdk"
tmp="$(mktemp -d)"
mkdir -p "$out"
bin="${CAPTURE_BIN:-$tmp/capture}"
[ -x "$bin" ] || (cd "$root" && go build -o "$bin" ./tools/sentry-fixtures/capture) || exit 1
port=$((20000 + RANDOM % 20000))
"$bin" -addr "127.0.0.1:$port" -out "$tmp/out" -sdk "$sdk" >"$tmp/capture.log" 2>&1 &
cap=$!
sleep 0.5
SENTRY_FIXTURE_DSN="http://0123456789abcdef0123456789abcdef@127.0.0.1:$port/42" "$@"
sleep 1
kill "$cap" 2>/dev/null; wait "$cap" 2>/dev/null
pick="${CAPTURE_PICK:-1}"
f="$(ls "$tmp"/out/*-"$type_".envelope 2>/dev/null | sort -t/ -k1 -V | sed -n "${pick}p")"
if [ -z "$f" ]; then
  echo "record-case: no '$type_' envelope captured for $sdk/$case_; got:" >&2
  ls "$tmp/out" >&2
  rm -rf "${tmp:?}"; exit 1
fi
cp "$f" "$out/$case_.envelope"
python3 -I - "${f%.envelope}.json" "$out/$case_.json" "$notes" <<'PY'
import json, sys
d = json.load(open(sys.argv[1])); d["notes"] = sys.argv[3]
open(sys.argv[2], "w").write(json.dumps(d, indent=2) + "\n")
PY
echo "recorded $sdk/$case_ ($(basename "$f"))"
rm -rf "${tmp:?}"
