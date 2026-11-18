#!/usr/bin/env bash
# Live release acceptance: run the real `sentry-cli releases ...` and `deploys ...`
# commands (npm @sentry/cli from the locked sourcemaps app) against a fresh Supermetry,
# inside a throwaway git repo with three commits, and check each step. Run under the
# heavy lock:
#   flock /tmp/claude-1000/supermetry-heavy.lock make sdk-compat-releases
# The CLI sees only SENTRY_URL, SENTRY_AUTH_TOKEN and the project. Nothing is recorded
# or committed. Only local git commits (set-commits --auto/--local) are supported.
set -uo pipefail
root="$(cd "$(dirname "$0")/../../.." && pwd)"
here="$root/tools/sentry-fixtures/live"
app="$here/sourcemaps"
. "$here/lib.sh"
sm_init
fail=0; pass=0
results=()
cleanup() { sm_stop; rm -rf "$app/node_modules" "${work:?}"; }
trap cleanup EXIT

record() { # name status detail
  results+=("$(printf '%-6s %-40s %s' "$2" "$1" "$3")")
  case "$2" in PASS) pass=$((pass+1));; *) fail=$((fail+1));; esac
}
clean_env() { env -i HOME="$home" PATH=/usr/bin:/bin:"$(dirname "$(command -v node)")" "$@"; }
cli_run() { # name expected-substring cmd... ; runs the CLI in $repo, records exit status and output match
  local name="$1" want="$2" out rc; shift 2
  out="$(cd "$repo" && clean_env SENTRY_URL="$base" SENTRY_AUTH_TOKEN="$token" SENTRY_PROJECT="$site" SENTRY_LOG_LEVEL="${REL_LOG_LEVEL:-warn}" "$cli" "$@" 2>&1)"; rc=$?
  last_out="$out"
  if [ $rc -ne 0 ]; then record "$name" FAIL "exit $rc: $(echo "$out" | grep -vi 'attach the full debug\|add --log-level' | tail -3 | tr '\n' ' ')"; return 1; fi
  if [ -n "$want" ] && ! grep -qi -- "$want" <<<"$out"; then record "$name" FAIL "output lacks '$want': $(echo "$out" | tail -2 | tr '\n' ' ')"; return 1; fi
  record "$name" PASS "$(echo "$out" | head -1)"
}
api() { # method path -> status code (body in $work/api.out)
  curl -sS -o "$work/api.out" -w '%{http_code}' -X "$1" -H "Authorization: Bearer $token" "$base$2"
}

df -h / | tail -1; free -h | sed -n 2p
echo "== deps"
(cd "$app" && bun install --frozen-lockfile >/dev/null) || { echo "cannot install @sentry/cli (no network or registry access)"; exit 1; }
(cd "$root" && go build -o "$work/supermetry" ./cmd/supermetry) || exit 1
cli="$app/node_modules/.bin/sentry-cli"
version="$("$cli" --version 2>&1)"
echo "tool: $version"

echo "== server"
sm_start "$work/supermetry" SUPERMETRY_SENTRY_IP_PER_MIN=100000 || exit 1
out="$(python3 -I "$here/check.py" uploadtoken "$base" "$state" releases)" || { echo "site setup: $out"; exit 1; }
read -r site key token <<<"$out"
org="$(python3 -I -c 'import sys,base64,json;t=sys.argv[1].split("_")[1];print(json.loads(base64.b64decode(t))["org"])' "$token")"

echo "== throwaway git repo"
repo="$work/repo"; mkdir -p "$repo"
git -C "$repo" init -q
git -C "$repo" config user.name "Release Fixture"
git -C "$repo" config user.email "fixture@example.com"
git -C "$repo" remote add origin https://example.invalid/acme/app.git
for n in 1 2 3; do
  echo "line $n" >> "$repo/file$n.txt"; git -C "$repo" add . && git -C "$repo" commit -q -m "commit $n"
done

rel="1.0.0"
echo "== sentry-cli releases / deploys"
cli_run "releases new" "" releases new "$rel"
cli_run "releases set-commits --auto" "" releases set-commits "$rel" --auto
if grep -qi "local git" <<<"$last_out"; then record "set-commits used the local git tree" PASS "found 'local git' in output"; else record "set-commits used the local git tree" FAIL "$(echo "$last_out" | tail -3 | tr '\n' ' ')"; fi
code="$(api GET "/api/0/organizations/$org/releases/$rel/")"
count="$(python3 -I -c 'import json,sys;print(json.load(open(sys.argv[1])).get("commitCount"))' "$work/api.out" 2>/dev/null)"
if [ "$code" = 200 ] && [ "$count" = 3 ]; then record "commitCount is 3 via API" PASS "commitCount=$count"; else record "commitCount is 3 via API" FAIL "http $code commitCount=$count"; fi
cli_run "releases info" "$rel" releases info "$rel"
cli_run "releases finalize" "" releases finalize "$rel"
cli_run "deploys new" "" deploys new -r "$rel" -e production -n "live acceptance"
cli_run "deploys list" "production" deploys list -r "$rel"
cli_run "releases list" "$rel" releases list
cli_run "releases archive" "" releases archive "$rel"
code="$(api GET "/api/0/organizations/$org/releases/$rel/")"
status="$(python3 -I -c 'import json,sys;print(json.load(open(sys.argv[1])).get("status"))' "$work/api.out" 2>/dev/null)"
if [ "$status" = archived ]; then record "status is archived via API" PASS "status=$status"; else record "status is archived via API" FAIL "http $code status=$status"; fi
cli_run "releases delete" "" releases delete "$rel"
code="$(api GET "/api/0/organizations/$org/releases/$rel/")"
if [ "$code" = 404 ]; then record "GET after delete is 404" PASS "http 404"; else record "GET after delete is 404" FAIL "http $code"; fi

echo "== results ($version)"
printf '%s\n' "${results[@]}"
echo "PASS=$pass FAIL=$fail"
[ "$fail" -eq 0 ]
