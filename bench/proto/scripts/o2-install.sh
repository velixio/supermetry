#!/usr/bin/env bash
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
proto_dir=$(CDPATH= cd -- "$script_dir/.." && pwd)
# shellcheck source=lib/common.sh
source "$script_dir/lib/common.sh"

usage() {
  cat <<'EOF'
Usage: o2-install.sh [--threshold-seconds N]

Run three clean install-to-first-pageview trials. Each trial builds proto,
creates a fresh supermetry_o2_* schema, starts the binary (auto-migrations), POSTs
one pageview, confirms it in SQL, and drops the schema. The median must be at
or below 120 seconds by default.

Environment:
  SUPERMETRY_TEST_DSN         base connection (default: local supermetry DB/role)
EOF
}

threshold=120
while (($#)); do
  case "$1" in
    --threshold-seconds) [[ $# -ge 2 ]] || ops_die 'missing threshold value'; threshold=$2; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) ops_die "unknown argument: $1" ;;
  esac
done
[[ "$threshold" =~ ^[0-9]+$ ]] && ((threshold > 0)) || ops_die 'threshold must be a positive integer'

for cmd in go psql curl mktemp sort awk; do ops_require "$cmd"; done
ops_check_admin

tmp=$(mktemp -d "${TMPDIR:-/tmp}/supermetry-o2.XXXXXX")
declare -a dbs=()
server_pid=''
cleanup() {
  local db
  ops_stop_process "$server_pid"
  for db in "${dbs[@]:-}"; do
    [[ -n "$db" ]] && ops_drop_db "$db" >/dev/null 2>&1 || true
  done
  rm -rf -- "$tmp"
}
trap cleanup EXIT INT TERM

declare -a elapsed=()
base_port=$((20000 + ($$ % 20000)))
for run in 1 2 3; do
  db="supermetry_o2_${UID}_$$_${run}"
  dbs+=("$db")
  bin="$tmp/proto-$run"
  log="$tmp/serve-$run.log"
  port=$((base_port + run))
  started_ns=$(date +%s%N)

  (cd "$proto_dir" && go build -buildvcs=false -trimpath -o "$bin" .)
  ops_create_db "$db"
  dsn=$(ops_dsn "$db")
  SUPERMETRY_DSN="$dsn" "$bin" serve --addr "127.0.0.1:$port" --geoip '' \
    --sites "$proto_dir/testdata/fixture_sites.json" >"$log" 2>&1 &
  server_pid=$!
  if ! ops_wait_http "http://127.0.0.1:$port" "$server_pid"; then
    sed -n '1,160p' "$log" >&2
    ops_die "run $run: server did not become ready"
  fi

  status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
    --max-time 10 --request POST "http://127.0.0.1:$port/api/event" \
    --header 'Content-Type: application/json' \
    --header 'User-Agent: Mozilla/5.0 O2-install-check' \
    --header 'X-Forwarded-For: 192.0.2.42' \
    --data '{"n":"pageview","u":"https://a.test/o2","d":"a.test","r":"","p":{}}')
  [[ "$status" == 202 ]] || ops_die "run $run: POST returned HTTP $status"
  ended_ns=$(date +%s%N)
  seconds=$(awk -v a="$started_ns" -v b="$ended_ns" 'BEGIN { printf "%.3f", (b-a)/1000000000 }')
  elapsed+=("$seconds")

  found=0
  for _ in $(seq 1 100); do
    found=$(psql -X --set=ON_ERROR_STOP=1 --tuples-only --no-align --dbname "$dsn" \
      --command "SELECT count(*) FROM events WHERE path = '/o2'")
    [[ "$found" == 1 ]] && break
    sleep 0.05
  done
  [[ "$found" == 1 ]] || ops_die "run $run: SQL found $found matching pageviews"
  migrations=$(psql -X --set=ON_ERROR_STOP=1 --tuples-only --no-align --dbname "$dsn" \
    --command 'SELECT count(*) FROM schema_migrations')
  ((migrations > 0)) || ops_die "run $run: startup applied no migrations"
  printf 'O2 run %d: %ss to HTTP 202; SQL pageviews=1; migrations=%s\n' "$run" "$seconds" "$migrations"

  ops_stop_process "$server_pid"
  server_pid=''
  ops_drop_db "$db"
  dbs[${#dbs[@]}-1]=''
done

median=$(printf '%s\n' "${elapsed[@]}" | sort -n | sed -n '2p')
printf 'O2 median: %ss (limit %ss)\n' "$median" "$threshold"
awk -v median="$median" -v limit="$threshold" 'BEGIN { exit !(median <= limit) }' ||
  ops_die "O2 median ${median}s exceeds ${threshold}s"
printf 'O2 PASS\n'
