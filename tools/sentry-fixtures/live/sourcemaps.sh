#!/usr/bin/env bash
# Live source map acceptance: build a small Vite app, upload its source maps to a
# fresh Supermetry with each real Sentry upload tool, run the built app in headless
# Chromium, let it throw, then check the upload and the symbolicated top frame.
# Run under the heavy lock:
#   flock /tmp/claude-1000/supermetry-heavy.lock make sdk-compat-sourcemaps
# The tools see only SENTRY_URL and SENTRY_AUTH_TOKEN (the Vite plugin: url and
# authToken). Each tool is reported separately. The frame check exits "SKIP" while
# the server has no symbolication stage. Nothing is recorded or committed.
set -uo pipefail
root="$(cd "$(dirname "$0")/../../.." && pwd)"
here="$root/tools/sentry-fixtures/live"
app="$here/sourcemaps"
. "$here/lib.sh"
sm_init
fail=0; skip=0; pass=0
results=()
cleanup() { sm_stop; rm -rf "$app/node_modules" "${work:?}"; }
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

df -h / | tail -1; free -h | sed -n 2p
echo "== deps"
(cd "$app" && bun install --frozen-lockfile >/dev/null) || exit 1
(cd "$root" && go build -o "$work/supermetry" ./cmd/supermetry) || exit 1
cli="$app/node_modules/.bin/sentry-cli"; vite="$app/node_modules/.bin/vite"
versions="sentry-cli $("$cli" --version 2>&1 | awk '{print $NF}'), @sentry/vite-plugin $(node -p "require('$app/node_modules/@sentry/vite-plugin/package.json').version"), sentry $(node -p "require('$app/node_modules/sentry/package.json').version")"
echo "tools: $versions"

echo "== server"
sm_start "$work/supermetry" SUPERMETRY_SENTRY_IP_PER_MIN=100000 SUPERMETRY_APP_SPIKE_PER_MIN=100000 || exit 1

# clean_env <cmd...>: a clean environment plus what the tool needs to run.
clean_env() { env -i HOME="$home" PATH=/usr/bin:/bin:"$(dirname "$(command -v node)")" "$@"; }

# stage <tool> <upload cmd...>: build the app, upload (inside $dir), run it, check.
# Variables set by the caller: site, key, token.
copy_app() { # dir
  mkdir -p "$1" && cp -r "$app/src" "$app/index.html" "$app/vite.config.mjs" "$app/package.json" "$1/" && ln -s "$app/node_modules" "$1/node_modules"
}
assess() { # tool dir site key release
  local tool="$1" dir="$2" site="$3" key="$4" rel="$5" n
  n="$(psql "$db_url" -qAtc "select count(*) from app_artifact_bundles b where b.state='ok' and exists (select 1 from app_artifact_files f where f.bundle_id=b.id and (f.site_id=$site or f.site_id is null) and f.debug_id is not null)")"
  if [ "${n:-0}" -ge 1 ]; then record "$tool upload+assemble" PASS "$n bundle(s) state=ok with debug-id files"; else record "$tool upload+assemble" FAIL "no assembled bundle for site $site"; return; fi
  clean_env SENTRY_FIXTURE_DSN="http://$key@127.0.0.1:$port/$site" SM_RELEASE="$rel" PLAYWRIGHT_BROWSERS_PATH="${PLAYWRIGHT_BROWSERS_PATH:-$HOME/.cache/ms-playwright}" \
    node "$app/app.mjs" "$dir/dist" >"$work/run-$tool.log" 2>&1 || { record "$tool app run" FAIL "$(tail -3 "$work/run-$tool.log")"; return; }
  check "$tool issue" python3 -I "$here/check.py" issues "$base" "$state" "$site" 25
  check "$tool top frame is checkout in src/cart.js:12" python3 -I "$here/check.py" frame "$base" "$state" "$site" 15 src/cart.js 12 checkout
}
newsite() { # name -> sets site key token
  local out; out="$(python3 -I "$here/check.py" uploadtoken "$base" "$state" "$1")" || { echo "site setup: $out"; return 1; }
  read -r site key token <<<"$out"
}

echo "== sentry-cli (Rust, npm @sentry/cli)"
if newsite cli; then
  rel="sm-cli@1.0.0"; d="$work/app-cli"; copy_app "$d"
  if (cd "$d" && "$vite" build >"$work/build-cli.log" 2>&1); then
    out="$(cd "$d" && clean_env SENTRY_URL="$base" SENTRY_AUTH_TOKEN="$token" SENTRY_PROJECT="$site" "$cli" sourcemaps inject dist 2>&1 \
       && clean_env SENTRY_URL="$base" SENTRY_AUTH_TOKEN="$token" SENTRY_PROJECT="$site" "$cli" sourcemaps upload --release "$rel" dist 2>&1)"; rc=$?
    echo "$out" | tail -5
    [ $rc -eq 0 ] || record "cli inject+upload" FAIL "exit $rc"
    [ $rc -eq 0 ] && assess cli "$d" "$site" "$key" "$rel"
  else record "cli build" FAIL "$(tail -3 "$work/build-cli.log")"; fi
else record "cli setup" FAIL "site or token"; fi

echo "== @sentry/vite-plugin build"
if newsite vite; then
  rel="sm-vite@1.0.0"; d="$work/app-vite"; copy_app "$d"
  out="$(cd "$d" && clean_env SM_VITE_PLUGIN=1 SM_URL="$base" SM_TOKEN="$token" SM_RELEASE="$rel" SENTRY_PROJECT="$site" "$vite" build 2>&1)"; rc=$?
  echo "$out" | tail -8
  [ $rc -eq 0 ] || record "vite build+upload" FAIL "exit $rc"
  [ $rc -eq 0 ] && assess vite "$d" "$site" "$key" "$rel"
else record "vite setup" FAIL "site or token"; fi

echo "== sentry (TypeScript CLI, npm sentry)"
if newsite ts; then
  rel="sm-ts@1.0.0"; d="$work/app-ts"; copy_app "$d"
  tsbin="$app/node_modules/.bin/sentry"
  if ! (cd "$d" && "$vite" build >"$work/build-ts.log" 2>&1); then record "ts build" FAIL "$(tail -3 "$work/build-ts.log")"
  elif ! clean_env "$tsbin" --help 2>&1 | grep -qi sourcemap; then record "ts upload" SKIP "sentry $(node -p "require('$app/node_modules/sentry/package.json').version") has no sourcemap command"
  else
    out="$(cd "$d" && clean_env SENTRY_URL="$base" SENTRY_AUTH_TOKEN="$token" SENTRY_PROJECT="$site" "$tsbin" sourcemap inject dist 2>&1 \
       && clean_env SENTRY_URL="$base" SENTRY_AUTH_TOKEN="$token" SENTRY_PROJECT="$site" "$tsbin" sourcemap upload --release "$rel" dist 2>&1)"; rc=$?
    echo "$out" | tail -5
    [ $rc -eq 0 ] || record "ts inject+upload" FAIL "exit $rc"
    [ $rc -eq 0 ] && assess ts "$d" "$site" "$key" "$rel"
  fi
else record "ts setup" FAIL "site or token"; fi

echo "== results ($versions)"
printf '%s\n' "${results[@]}"
echo "PASS=$pass SKIP=$skip FAIL=$fail"
[ "$fail" -eq 0 ]
