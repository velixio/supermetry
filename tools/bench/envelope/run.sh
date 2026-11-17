#!/usr/bin/env bash
# Envelope endpoint load test on a scratch database. Run under the heavy lock:
#   ENVELOPE_BENCH_MINUTES=3 flock /tmp/claude-1000/supermetry-heavy.lock make envelope-bench
# ENVELOPE_BENCH_SITES (default 300) app sites with one DSN key each carry the
# load, because the per-key budget is 600 events a minute.
set -uo pipefail
root="$(cd "$(dirname "$0")/../../.." && pwd)"
. "$root/tools/sentry-fixtures/live/lib.sh"
sm_init
cleanup() { sm_stop; rm -rf "${work:?}"; }
trap cleanup EXIT
df -h / | tail -1; free -h | sed -n 2p
(cd "$root" && go build -o "$work/supermetry" ./cmd/supermetry && go build -o "$work/envelope-bench" ./tools/bench/envelope) || exit 1
sm_start "$work/supermetry" SUPERMETRY_SENTRY_IP_PER_MIN=100000 SUPERMETRY_APP_SPIKE_PER_MIN=1000000 || exit 1
python3 -I "$root/tools/sentry-fixtures/live/check.py" sites "$base" "$state" "${ENVELOPE_BENCH_SITES:-300}" >"$work/keys.txt" || exit 1
"$work/envelope-bench" -base "$base" -keys "$work/keys.txt" -fixtures "$root/internal/sentryingest/testdata" \
  -minutes "${ENVELOPE_BENCH_MINUTES:-5}" -workers "${ENVELOPE_BENCH_WORKERS:-24}"
rc=$?
sleep 3
echo "server errors in log: $(grep -ci 'level=error' "$work/server.log")"
psql "$db_url" -Atc "select 'app_events rows: '||count(*) from app_events" 2>/dev/null
exit $rc
