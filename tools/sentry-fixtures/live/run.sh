#!/usr/bin/env bash
# Live SDK acceptance: point the real Sentry SDK generators (Node, browser,
# Python, Go) at a freshly started Supermetry by changing only the DSN, then
# check the private API. Run under the heavy lock:
#   flock /tmp/claude-1000/supermetry-heavy.lock make sdk-compat
# The server runs on a scratch database (created on the dev Postgres, dropped
# at the end). SDK processes get a clean environment so nothing from this
# machine reaches them. Nothing is recorded or committed.
set -uo pipefail
root="$(cd "$(dirname "$0")/../../.." && pwd)"
fx="$root/tools/sentry-fixtures"
here="$fx/live"
. "$here/lib.sh"
sm_init
fail=0; skip=0; pass=0
results=()

cleanup() {
  sm_stop
  rm -rf "$fx/node/node_modules" "$fx/browser/node_modules" "$fx/browser/dist" "$fx/python/.venv" "${work:?}" /tmp/fixture-app
}
trap cleanup EXIT

df -h / | tail -1; free -h | sed -n 2p

echo "== deps"
(cd "$fx/node" && bun install --frozen-lockfile >/dev/null) || exit 1
(cd "$fx/browser" && bun install --frozen-lockfile >/dev/null && bun build client.mjs --outfile dist/client.js >/dev/null) || exit 1
(cd "$fx/python" && uv venv -q .venv && uv pip install -q --python .venv/bin/python --require-hashes -r requirements.txt) || exit 1
(cd "$fx/go" && go build -trimpath -o "$work/fixture-go" .) || exit 1
(cd "$root" && go build -o "$work/supermetry" ./cmd/supermetry) || exit 1

echo "== server"
sm_start "$work/supermetry" SUPERMETRY_SENTRY_IP_PER_MIN=100000 SUPERMETRY_APP_SPIKE_PER_MIN=100000 || exit 1

# Environment for SDK processes: nothing but the DSN and what a runtime needs.
sdk() { # dsn cwd cmd...
  local dsn="$1" dir="$2"; shift 2
  (cd "$dir" && env -i HOME="$home" PATH=/usr/bin:/bin SENTRY_FIXTURE_DSN="$dsn" \
      PLAYWRIGHT_BROWSERS_PATH="${PLAYWRIGHT_BROWSERS_PATH:-$HOME/.cache/ms-playwright}" "$@") >"$work/sdk.log" 2>&1
  return 0 # crash cases exit non-zero by design
}

record() { # name status detail
  results+=("$(printf '%-8s %-36s %s' "$2" "$1" "$3")")
  case "$2" in PASS) pass=$((pass+1));; SKIP) skip=$((skip+1));; *) fail=$((fail+1));; esac
}

check() { # name cmd...
  local name="$1" out rc; shift
  out="$("$@" 2>&1)"; rc=$?
  case $rc in 0) record "$name" PASS "$out";; 3) record "$name" SKIP "$out";; *) record "$name" FAIL "$out";; esac
}

# run_case <sdk/case> <expect: issues|sessions|both|feedback|none> <breadcrumbs 0/1> <dir> <cmd...>
run_case() {
  local label="$1" expect="$2" crumbs="$3" dir="$4"; shift 4
  local out id key
  out="$(python3 -I "$here/check.py" site "$base" "$state" "${label//\//-}")" || { record "$label" FAIL "site setup: $out"; return; }
  read -r id key <<<"$out"
  sdk "http://$key@127.0.0.1:$port/$id" "$dir" "$@"
  case "$expect" in issues|both|feedback) check "$label issue" python3 -I "$here/check.py" issues "$base" "$state" "$id" 25;; esac
  case "$expect" in sessions|both) check "$label session" python3 -I "$here/check.py" sessions "$base" "$state" "$id" 25 "$db_url";; esac
  [ "$expect" = feedback ] && check "$label feedback" python3 -I "$here/check.py" feedback "$base" "$state" "$id" 25 "$db_url"
  [ "$crumbs" = 1 ] && check "$label breadcrumbs" python3 -I "$here/check.py" breadcrumbs "$base" "$state" "$id" "$db_url"
  return 0
}

echo "== SDK runs"
NODE=(/usr/bin/node --import @sentry/node/import app.mjs)
run_case node/unhandled-exception issues 0 "$fx/node" "${NODE[@]}" unhandled-exception
run_case node/unhandled-rejection issues 0 "$fx/node" "${NODE[@]}" unhandled-rejection
run_case node/captured-message    issues 0 "$fx/node" "${NODE[@]}" captured-message
run_case node/session-aggregates  sessions 0 "$fx/node" "${NODE[@]}" session-aggregates
for c in onerror unhandledrejection; do
  run_case "browser/$c" issues 0 "$fx/browser" /usr/bin/node app.mjs "$c"
done
run_case browser/breadcrumbs issues 1 "$fx/browser" /usr/bin/node app.mjs breadcrumbs
run_case browser/feedback feedback 0 "$fx/browser" /usr/bin/node app.mjs feedback
run_case browser/session sessions 0 "$fx/browser" /usr/bin/node app.mjs session
run_case python/unhandled-exception issues 0 "$fx/python" /bin/bash run.sh unhandled-exception
run_case python/logging-error       issues 0 "$fx/python" /bin/bash run.sh logging-error
run_case python/wsgi-sessions       sessions 0 "$fx/python" /bin/bash run.sh wsgi-sessions
run_case go/captured-exception issues 0 "$fx/go" "$work/fixture-go" captured-exception
run_case go/recovered-panic    issues 0 "$fx/go" "$work/fixture-go" recovered-panic

echo "== results"
printf '%s\n' "${results[@]}"
echo "PASS=$pass SKIP=$skip FAIL=$fail"
[ "$fail" -eq 0 ]
