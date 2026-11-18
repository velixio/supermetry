#!/usr/bin/env bash
# Live Electron native-crash acceptance: a real @sentry/electron app (Electron 40
# under xvfb) with only the DSN pointed at a fresh Supermetry. Run under the
# heavy lock:
#   flock /tmp/claude-1000/supermetry-heavy.lock make sdk-compat-electron
# Needs network: the first native crash makes the server fetch Electron's public
# symbols (symbols.electronjs.org, ~0.75 GB for the linux x64 binary) and
# Electron itself is downloaded by `bun install` the first time. The app gets a
# clean environment (env -i); nothing is recorded or committed.
#   1. native-crash: process.crash() in the main process, relaunch, the SDK sends
#      the minidump. The first crash of an Electron build arrives before its
#      symbols are cached (the dump is never kept, so that event stays
#      unsymbolicated); the script waits for the server to finish fetching them,
#      crashes the app again, and polls the private API until a crash issue
#      appears whose top frame has a function name (not electron+0x...).
#   2. consent-denied: the same with beforeSend returning null. After the
#      relaunch the site must stay empty for 30 s (Review Focus 4).
set -uo pipefail
root="$(cd "$(dirname "$0")/../../.." && pwd)"
fx="$root/tools/sentry-fixtures"
here="$fx/live"
app="$fx/electron"
. "$here/lib.sh"
sm_init
fail=0; skip=0; pass=0
results=()
cleanup() {
  sm_stop
  rm -rf "$app/node_modules" "${work:?}" /tmp/fixture-app /tmp/fixture-home
}
trap cleanup EXIT

record() { # name status detail
  results+=("$(printf '%-6s %-34s %s' "$2" "$1" "$3")")
  case "$2" in PASS) pass=$((pass+1));; SKIP) skip=$((skip+1));; *) fail=$((fail+1));; esac
}
check() { # name cmd...
  local name="$1" out rc; shift
  out="$("$@" 2>&1)"; rc=$?
  case $rc in 0) record "$name" PASS "$out";; 3) record "$name" SKIP "$out";; *) record "$name" FAIL "$out";; esac
}

df -h / /tmp | tail -2; free -h | sed -n 2p
command -v xvfb-run >/dev/null || { echo "xvfb-run is required"; exit 1; }
echo "== deps"
(cd "$app" && bun install --frozen-lockfile >/dev/null) || exit 1
(cd "$root" && go build -o "$work/supermetry" ./cmd/supermetry) || exit 1
echo "electron $(node -p "require('$app/node_modules/electron/package.json').version"), @sentry/electron $(node -p "require('$app/node_modules/@sentry/electron/package.json').version")"

echo "== server"
sm_start "$work/supermetry" SUPERMETRY_SENTRY_IP_PER_MIN=100000 SUPERMETRY_APP_SPIKE_PER_MIN=100000 SUPERMETRY_ELECTRON_SYMBOLS=true || exit 1

scenario() { # name wait-for
  local name="$1" out id key
  out="$(python3 -I "$here/check.py" site "$base" "$state" "electron-$name")" || { record "electron/$name" FAIL "site setup: $out"; return 1; }
  read -r id key <<<"$out"
  SENTRY_FIXTURE_DSN="http://$key@127.0.0.1:$port/$id" bash "$app/native-crash.sh" "$name" >"$work/electron-$name.log" 2>&1
  site="$id"; dsn_key="$key"
}

echo "== Electron runs"
if scenario native-crash; then
  # Warm-up crash done. The server downloads and converts a ~0.75 GB symbol file
  # in the background on the first miss; wait for it, then crash again.
  for _ in $(seq 1 120); do
    [ "$(psql "$db_url" -qAtc "select count(*) from app_remote_symbols where state = 'ok'")" -ge 1 ] && break
    sleep 5
  done
  n="$(psql "$db_url" -qAtc "select count(*) from app_remote_symbols where state = 'ok'")"
  record "electron/native-crash symbols fetched" "$([ "${n:-0}" -ge 1 ] && echo PASS || echo FAIL)" "$(psql "$db_url" -qAtc "select source || ' ' || debug_file || ' ' || debug_id || ' ' || state || ' ' || size || ' bytes' from app_remote_symbols order by 1 limit 3" | tr '\n' ';')"
  SENTRY_FIXTURE_DSN="http://$dsn_key@127.0.0.1:$port/$site" bash "$app/native-crash.sh" native-crash >>"$work/electron-native-crash.log" 2>&1
  check "electron/native-crash top frame named" python3 -I "$here/check.py" nativecrash "$base" "$state" "$site" 60
fi
if scenario consent-denied; then
  check "electron/consent-denied sends nothing" python3 -I "$here/check.py" silent "$base" "$state" "$site" 30
fi

echo "== results"
printf '%s\n' "${results[@]}"
echo "PASS=$pass SKIP=$skip FAIL=$fail"
[ "$fail" -eq 0 ]
