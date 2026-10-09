#!/usr/bin/env bash
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
proto_dir=$(CDPATH= cd -- "$script_dir/.." && pwd)
bench_dir=$(CDPATH= cd -- "$proto_dir/.." && pwd)
# shellcheck source=lib/common.sh
source "$script_dir/lib/common.sh"

usage() {
  cat <<EOF
Usage: o4-backup.sh [--sites FILE] [--events FILE] [--truth FILE]

Load a fixture into an isolated supermetry_o4_* schema, close its date range,
create a schema-scoped pg_dump archive, drop it, restore into a fresh schema
with pg_restore, and run proto verify (must report 0 mismatches).

Defaults:
  --sites  $bench_dir/gen/testdata/tiny.sites.json
  --events $bench_dir/gen/testdata/tiny.ndjson
  --truth  $bench_dir/gen/testdata/tiny.truth.json

Environment: SUPERMETRY_TEST_DSN (base connection; isolated schemas are used)
EOF
}

sites="$bench_dir/gen/testdata/tiny.sites.json"
events="$bench_dir/gen/testdata/tiny.ndjson"
truth="$bench_dir/gen/testdata/tiny.truth.json"
while (($#)); do
  case "$1" in
    --sites) [[ $# -ge 2 ]] || ops_die 'missing --sites value'; sites=$2; shift 2 ;;
    --events) [[ $# -ge 2 ]] || ops_die 'missing --events value'; events=$2; shift 2 ;;
    --truth) [[ $# -ge 2 ]] || ops_die 'missing --truth value'; truth=$2; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) ops_die "unknown argument: $1" ;;
  esac
done
for file in "$sites" "$events" "$truth"; do [[ -r "$file" ]] || ops_die "fixture is not readable: $file"; done
for cmd in go psql pg_dump pg_restore mktemp; do ops_require "$cmd"; done
ops_check_admin

tmp=$(mktemp -d "${TMPDIR:-/tmp}/supermetry-o4.XXXXXX")
src_db="supermetry_o4_${UID}_$$_src"
cleanup() {
  ops_drop_db "$src_db" >/dev/null 2>&1 || true
  rm -rf -- "$tmp"
}
trap cleanup EXIT INT TERM

bin="$tmp/proto"
dump="$tmp/supermetry.dump"
(cd "$proto_dir" && go build -buildvcs=false -trimpath -o "$bin" .)

ops_create_db "$src_db"
src_dsn=$(ops_dsn "$src_db")
SUPERMETRY_DSN="$src_dsn" "$bin" load --sites "$sites" < "$events" >/dev/null
read -r from to < <(psql -X --set=ON_ERROR_STOP=1 --tuples-only --no-align --field-separator=' ' \
  --dbname "$src_dsn" --command 'SELECT min(local_date), max(local_date) FROM events')
[[ -n "$from" && -n "$to" ]] || ops_die 'fixture loaded no events'
SUPERMETRY_DSN="$src_dsn" "$bin" rollup --from "$from" --to "$to" >/dev/null

pg_dump --format=custom --no-owner --no-privileges --schema "$src_db" --file "$dump" --dbname "$src_dsn"
[[ -s "$dump" ]] || ops_die 'pg_dump produced an empty archive'

src_events=$(psql -X --set=ON_ERROR_STOP=1 --tuples-only --no-align --dbname "$src_dsn" \
  --command 'SELECT count(*) FROM events')
ops_drop_db "$src_db"
pg_restore --exit-on-error --single-transaction --no-owner --no-privileges \
  --dbname "$(ops_base_dsn)" "$dump"

if ! verify_output=$(SUPERMETRY_DSN="$src_dsn" "$bin" verify --truth "$truth" --workers 4 2>&1); then
  printf '%s\n' "$verify_output" >&2
  ops_die 'proto verify failed against the restored database'
fi
printf '%s\n' "$verify_output"
grep -Eq '"mismatches"[[:space:]]*:[[:space:]]*0' <<<"$verify_output" ||
  ops_die 'restored verification did not report 0 mismatches'

restored_events=$(psql -X --set=ON_ERROR_STOP=1 --tuples-only --no-align --dbname "$src_dsn" \
  --command 'SELECT count(*) FROM events')
[[ "$src_events" == "$restored_events" ]] || ops_die 'restored event count differs from source'

printf 'O4 PASS: custom pg_dump/pg_restore preserved %s events; proto verify reported 0 mismatches\n' \
  "$restored_events"
