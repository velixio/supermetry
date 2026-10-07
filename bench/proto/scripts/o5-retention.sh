#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
schema="panel_o5_${$}"
db_dsn=${PANEL_TEST_DSN:-postgres://panel:panel@127.0.0.1:5432/panel?sslmode=disable}
export PGPASSWORD=${PGPASSWORD:-panel}
export PGOPTIONS="-c search_path=$schema"
cleanup() {
  PGOPTIONS= psql "$db_dsn" -v ON_ERROR_STOP=1 -c "DROP SCHEMA IF EXISTS $schema CASCADE" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

PGOPTIONS= psql "$db_dsn" -v ON_ERROR_STOP=1 -c "CREATE SCHEMA $schema" >/dev/null
(cd "$root" && go build -o "$tmp/proto" .)
(cd "$root/../gen" && go build -o "$tmp/gen" .)

printf '{"line":"O5","raw_days":7,"partition":"daily","samples":['
declare -a steady_raw=()
for offset in $(seq 0 20); do
  day=$(date -u -d "2026-07-01 +${offset} days" +%F)
  "$tmp/gen" --tier tiny --days 1 --start "$day" --seed 42 --out "$tmp/day.ndjson" \
    --sites "$tmp/sites.json" --truth "$tmp/truth.json" --quiet
  PANEL_DSN="$db_dsn" PANEL_RAW_PARTITION=daily "$tmp/proto" load --sites="$tmp/sites.json" \
    --batch=2000 --flush=20ms <"$tmp/day.ndjson" >/dev/null
  PANEL_DSN="$db_dsn" PANEL_RAW_PARTITION=daily "$tmp/proto" rollup --from "$day" --to "$day" --parallel=1 >/dev/null
  PANEL_DSN="$db_dsn" PANEL_RAW_PARTITION=daily "$tmp/proto" retention --raw-days 7 --as-of "$day" >/dev/null
  raw_bytes=$(psql "$db_dsn" -Atc "select coalesce(sum(pg_total_relation_size(inhrelid)),0) from pg_inherits where inhparent='events'::regclass")
  db_bytes=$(psql "$db_dsn" -Atc "select coalesce(sum(pg_total_relation_size(c.oid)),0) from pg_class c join pg_namespace n on n.oid=c.relnamespace where n.nspname=current_schema() and c.relkind in ('r','p')")
  parts=$(psql "$db_dsn" -Atc "select count(*) from pg_inherits where inhparent='events'::regclass")
  rollups=$(psql "$db_dsn" -Atc 'select count(*) from rollup_day')
  if ((offset >= 7)); then steady_raw+=("$raw_bytes"); fi
  [[ $offset == 0 ]] || printf ','
  printf '{"day":"%s","raw_bytes":%s,"schema_bytes":%s,"raw_partitions":%s,"rollup_rows":%s}' "$day" "$raw_bytes" "$db_bytes" "$parts" "$rollups"
done
last_parts=$(psql "$db_dsn" -Atc "select count(*) from pg_inherits where inhparent='events'::regclass")
kept_rollups=$(psql "$db_dsn" -Atc 'select count(*) from rollup_day')
steady_min=$(printf '%s\n' "${steady_raw[@]}" | sort -n | head -1)
steady_max=$(printf '%s\n' "${steady_raw[@]}" | sort -n | tail -1)
steady_variation=$(awk -v lo="$steady_min" -v hi="$steady_max" 'BEGIN { printf "%.2f", 100*(hi-lo)/lo }')
pass=false
if [[ $last_parts -le 9 && $kept_rollups -gt 0 ]] && awk -v lo="$steady_min" -v hi="$steady_max" 'BEGIN { exit !(hi <= lo*1.10) }'; then pass=true; fi
printf '],"steady_raw_bytes_min":%s,"steady_raw_bytes_max":%s,"steady_variation_percent":%s,"final_raw_partitions":%s,"closed_rollup_rows":%s,"pass":%s,"isolation":"schema:%s","note":"raw-event storage plateaus; total isolated schema retains closed-tier tables and rollups"}\n' \
  "$steady_min" "$steady_max" "$steady_variation" "$last_parts" "$kept_rollups" "$pass" "$schema"
[[ $pass == true ]]
