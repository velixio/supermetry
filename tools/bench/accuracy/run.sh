#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
SUPERMETRY="$ROOT/bin/supermetry"
BASE_DSN=${SUPERMETRY_TEST_DSN:-postgres://supermetry:supermetry@127.0.0.1:5432/supermetry?sslmode=disable}
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
SCHEMA="bench_accuracy_$(printf '%s' "$STAMP" | tr '[:upper:]' '[:lower:]' | tr -c '0-9a-z' '_')_$$"
TMP_ROOT="$ROOT/tools/bench/results/tmp"
WORK="$TMP_ROOT/accuracy-$STAMP-$$"
RESULT="$ROOT/tools/bench/results/accuracy-$STAMP.md"
SERVER_PID=
WATCHER_PID=
ACTIVE_PID_FILE="$WORK/active.pid"
MIN_START_GB=${ACCURACY_MIN_START_GB:-35}
MIN_RUN_GB=${ACCURACY_MIN_RUN_GB:-15}
MAIN_PID=$BASHPID

for command in curl free go psql python3; do
  command -v "$command" >/dev/null || { echo "missing command: $command" >&2; exit 1; }
done
[[ -x $SUPERMETRY ]] || { echo "build bin/supermetry first with make build" >&2; exit 1; }
mkdir -p "$WORK"

available_disk_gb() {
  df --output=avail -B1G / | tail -1 | tr -d '[:space:]'
}

abort_low_disk() {
  available=$1
  printf 'aborted: low disk (%s GB available; minimum is %s GB)\n' "$available" "$MIN_RUN_GB" | tee "$RESULT" >&2
}

START_DISK_GB=$(available_disk_gb)
if (( START_DISK_GB < MIN_START_GB )); then
  printf 'aborted: low disk (%s GB available; refusing to start below %s GB)\n' "$START_DISK_GB" "$MIN_START_GB" | tee "$RESULT" >&2
  rm -rf -- "$WORK"
  exit 1
fi

stop_server() {
  if [[ -n ${SERVER_PID:-} ]]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    SERVER_PID=
    clear_active
  fi
}

cleanup() {
  status=${1:-$?}
  trap - EXIT INT TERM
  set +e
  if [[ -n ${WATCHER_PID:-} ]]; then
    kill "$WATCHER_PID" 2>/dev/null || true
    wait "$WATCHER_PID" 2>/dev/null || true
    WATCHER_PID=
  fi
  if [[ -s $ACTIVE_PID_FILE ]]; then
    read -r active_pid <"$ACTIVE_PID_FILE"
    kill "$active_pid" 2>/dev/null || true
    wait "$active_pid" 2>/dev/null || true
  fi
  stop_server
  env -u PGOPTIONS psql "$BASE_DSN" -v ON_ERROR_STOP=1 -c "drop schema if exists \"$SCHEMA\" cascade" >/dev/null 2>&1
  rm -rf -- "$WORK"
  exit "$status"
}
trap cleanup EXIT
trap 'cleanup 130' INT
trap 'cleanup 143' TERM

echo "accuracy: remove leftover schemas owned by this runner"
while IFS= read -r stale_schema; do
  [[ -z $stale_schema ]] && continue
  if [[ $stale_schema =~ ^bench_accuracy_[a-z0-9_]+$ ]]; then
    env -u PGOPTIONS psql "$BASE_DSN" -v ON_ERROR_STOP=1 \
      -c "drop schema if exists \"$stale_schema\" cascade" >/dev/null
  fi
done < <(env -u PGOPTIONS psql "$BASE_DSN" -At -v ON_ERROR_STOP=1 \
  -c "select nspname from pg_namespace where nspname ~ '^bench_accuracy_[a-z0-9_]+$'")

env -u PGOPTIONS psql "$BASE_DSN" -v ON_ERROR_STOP=1 -c "create schema \"$SCHEMA\"" >/dev/null
export PGOPTIONS="-csearch_path=$SCHEMA"
export SUPERMETRY_DATABASE_URL="$BASE_DSN"
export SUPERMETRY_BASE_URL=http://127.0.0.1:18082
export SUPERMETRY_LISTEN=127.0.0.1:18082
export SUPERMETRY_SECRET_KEY=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
export SUPERMETRY_ADMIN_API_KEY=accuracy-check-key
export SUPERMETRY_TRUSTED_PROXIES=127.0.0.1
export SUPERMETRY_GEOIP_PATH="$ROOT/bench/proto/data/dbip-country-lite.mmdb"
export SUPERMETRY_QUERY_TIMEOUT=30s
export SUPERMETRY_FLUSH_EVERY=20ms
export SUPERMETRY_SESSION_FLUSH=20ms

watch_disk() {
  while sleep 5; do
    available=$(available_disk_gb)
    if (( available < MIN_RUN_GB )); then
      abort_low_disk "$available"
      if [[ -s $ACTIVE_PID_FILE ]]; then
        read -r active_pid <"$ACTIVE_PID_FILE"
        kill -TERM "$active_pid" 2>/dev/null || true
      fi
      kill -TERM "$MAIN_PID" 2>/dev/null || true
      return 1
    fi
  done
}

set_active() {
  printf '%s\n' "$1" >"$ACTIVE_PID_FILE"
}

clear_active() {
  : >"$ACTIVE_PID_FILE"
}

watch_disk &
WATCHER_PID=$!

echo "accuracy: build deterministic generator"
(cd "$ROOT/tools/gen" && go build -o "$WORK/gen" .)

echo "accuracy: generate 14 days across a month boundary"
"$WORK/gen" --tier tiny --days 14 --start 2026-09-24 --seed 42 --workers 1 \
  --events-per-day 1000 --out "$WORK/all.ndjson" --sites "$WORK/gen-sites.json" \
  --truth "$WORK/gen-truth.json" --quiet &
set_active "$!"
wait "$!"
clear_active
python3 "$ROOT/tools/bench/accuracy/prepare.py" \
  --source-sites "$WORK/gen-sites.json" --source-events "$WORK/all.ndjson" \
  --sites "$WORK/sites.json" --events "$WORK/events.ndjson" --site-ids 1,3,5
export SUPERMETRY_SITES_FILE="$WORK/sites.json"

echo "accuracy: load product"
LOAD_DISK_GB=$(available_disk_gb)
if (( LOAD_DISK_GB < MIN_START_GB )); then
  printf 'aborted: low disk (%s GB available; refusing load below %s GB)\n' "$LOAD_DISK_GB" "$MIN_START_GB" | tee "$RESULT" >&2
  exit 1
fi
"$SUPERMETRY" load <"$WORK/events.ndjson" >"$WORK/load.json" 2>"$WORK/load.log" &
set_active "$!"
wait "$!"
clear_active

start_server() {
  "$SUPERMETRY" serve >"$WORK/server-$1.log" 2>&1 &
  SERVER_PID=$!
  set_active "$SERVER_PID"
  for _ in $(seq 1 120); do
    if curl -fsS "$SUPERMETRY_BASE_URL/readyz" >/dev/null 2>&1; then
      return
    fi
    kill -0 "$SERVER_PID" 2>/dev/null || { tail -80 "$WORK/server-$1.log" >&2; exit 1; }
    sleep 0.25
  done
  echo "Supermetry did not become ready" >&2
  exit 1
}

echo "accuracy: compare hot tier"
start_server hot
set +e
python3 "$ROOT/tools/bench/accuracy/check.py" \
  --events "$WORK/events.ndjson" --sites "$WORK/sites.json" \
  --base-url "$SUPERMETRY_BASE_URL" --key "$SUPERMETRY_ADMIN_API_KEY" --phase hot \
  --from 2026-09-24 --to 2026-10-07 --output "$WORK/hot.json" &
set_active "$!"
wait "$!"
HOT_STATUS=$?
clear_active
set -e
stop_server

echo "accuracy: close all selected site-days"
for site in 1 3 5; do
  "$SUPERMETRY" close-days --site "$site" --from 2026-09-24 --to 2026-10-07 >"$WORK/close-$site.log" 2>&1 &
  set_active "$!"
  wait "$!"
  clear_active
done
psql "$BASE_DSN" -v ON_ERROR_STOP=1 -c \
  'analyze sessions_closed, custom_closed, cube_day, visitor_day, rollup_day, rollup_day_dim, day_close, dict' >/dev/null

echo "accuracy: compare closed and rollup tiers"
start_server closed
set +e
python3 "$ROOT/tools/bench/accuracy/check.py" \
  --events "$WORK/events.ndjson" --sites "$WORK/sites.json" \
  --base-url "$SUPERMETRY_BASE_URL" --key "$SUPERMETRY_ADMIN_API_KEY" --phase closed \
  --from 2026-09-24 --to 2026-10-07 --output "$WORK/closed.json" &
set_active "$!"
wait "$!"
CLOSED_STATUS=$?
clear_active
set -e
stop_server

AVAILABLE_GB=$(free -g | awk '/^Mem:/ {print $7}')
LAYER2_DISK_GB=$(available_disk_gb)
if ! command -v docker >/dev/null 2>&1; then
  printf '%s\n' 'SKIPPED: Docker is not installed.' >"$WORK/layer2.txt"
elif (( AVAILABLE_GB < 4 )); then
  printf 'SKIPPED: only %s GB of memory is available; Layer 2 requires at least 4 GB.\n' "$AVAILABLE_GB" >"$WORK/layer2.txt"
elif (( LAYER2_DISK_GB < MIN_START_GB )); then
  printf 'SKIPPED: only %s GB of disk is available; Layer 2 requires at least %s GB.\n' "$LAYER2_DISK_GB" "$MIN_START_GB" >"$WORK/layer2.txt"
elif ! docker info >/dev/null 2>"$WORK/docker-error.log"; then
  printf '%s\n' 'SKIPPED: Docker is installed but the daemon is unavailable to this user.' >"$WORK/layer2.txt"
else
  printf '%s\n' 'SKIPPED: the optional compressed live replay was not run automatically; use the Layer 3 kit for a tracker-level comparison.' >"$WORK/layer2.txt"
fi

set +e
python3 "$ROOT/tools/bench/accuracy/report.py" --stamp "$STAMP" --hot "$WORK/hot.json" \
  --closed "$WORK/closed.json" --layer2 "$WORK/layer2.txt" --output "$RESULT"
REPORT_STATUS=$?
set -e
echo "accuracy: result $RESULT"
if (( HOT_STATUS != 0 || CLOSED_STATUS != 0 || REPORT_STATUS != 0 )); then
  exit 1
fi
