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
# ENVELOPE_BENCH_RULES=N gives every bench site N ownership and N grouping rules.
# All but the last ownership rule miss, so each event pays for a full scan; the last
# one (path "*") matches, so every new issue is also assigned. The registry reloads
# rules every 30 s, so wait that out before the load starts.
rules="${ENVELOPE_BENCH_RULES:-0}"
if [ "$rules" -gt 0 ]; then
  psql "$db_url" -qAt <<SQL || exit 1
insert into app_ownership_rules (site_id, position, matcher, value, owner_user_id)
  select s.id, g, 'path', case when g = $rules - 1 then '*' else 'nomatch/pkg' || g || '/*/file?.go' end, (select min(id) from users)
  from sites s, generate_series(0, $rules - 1) g where s.kind = 'app';
insert into app_grouping_rules (site_id, position, matcher, value, fingerprint)
  select s.id, g, (array['error_type', 'message', 'path', 'module', 'tag:team'])[1 + g % 5], 'nomatch-' || g || '-*', 'fp' || g
  from sites s, generate_series(0, $rules - 1) g where s.kind = 'app';
SQL
  echo "configured $rules ownership and $rules grouping rules per site; waiting for the registry refresh"
  sleep 35
fi
"$work/envelope-bench" -base "$base" -keys "$work/keys.txt" -fixtures "$root/internal/sentryingest/testdata" \
  -minutes "${ENVELOPE_BENCH_MINUTES:-5}" -workers "${ENVELOPE_BENCH_WORKERS:-24}" ${ENVELOPE_BENCH_OTLP:+-otlp} ${ENVELOPE_BENCH_OTLP_METRICS:+-otlp-metrics}
rc=$?
sleep 3
echo "server errors in log: $(grep -ci 'level=error' "$work/server.log")"
psql "$db_url" -Atc "select 'app_events rows: '||count(*) from app_events" 2>/dev/null
psql "$db_url" -Atc "select 'spans rows: '||count(*) from spans" 2>/dev/null
psql "$db_url" -Atc "select 'metric_points rows: '||count(*) from metric_points" 2>/dev/null
exit $rc
