#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: bench/product/run.sh tier-s" >&2
  exit 2
}

[[ ${1:-} == tier-s && $# == 1 ]] || usage

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
SUPERMETRY="$ROOT/bin/supermetry"
THRESHOLDS="$ROOT/bench/product/thresholds.json"
BASE_DSN=${SUPERMETRY_TEST_DSN:-postgres://supermetry:supermetry@127.0.0.1:5432/supermetry?sslmode=disable}
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
RESULT_BASE="$ROOT/bench/results/product-$STAMP"
RESULT="$RESULT_BASE.json"
REPORT="$RESULT_BASE.md"
TMP_ROOT="$ROOT/bench/results/tmp"
mkdir -p "$TMP_ROOT"
WORK=$(mktemp -d "$TMP_ROOT/product.XXXXXX")
SCHEMA_PREFIX=bench_product_
SCHEMA="${SCHEMA_PREFIX}$(printf '%s' "$STAMP" | tr '[:upper:]' '[:lower:]' | tr -c '0-9a-z' '_')_$$"
SERVER_PID=
SAMPLER_PID=
WATCHER_PID=
LOW_DISK_MARKER="$WORK/aborted-low-disk"
MIN_START_GB=35
MIN_FREE_GB=15
FULL_DATA_GB=45

for command in python3 curl psql go pmap; do
  command -v "$command" >/dev/null || { echo "missing command: $command" >&2; exit 1; }
done
[[ -x $SUPERMETRY ]] || { echo "build bin/supermetry first with make build" >&2; exit 1; }
[[ -f $ROOT/internal/enrich/lists/dbip-country-lite.mmdb ]] || { echo "missing bundled GeoIP test database" >&2; exit 1; }

drop_own_schema() {
  env -u PGOPTIONS psql "$BASE_DSN" -v ON_ERROR_STOP=1 -c "drop schema if exists \"$SCHEMA\" cascade" >/dev/null 2>&1
}

cleanup() {
  status=$?
  trap - EXIT INT TERM
  set +e
  if (( status != 0 )) && [[ -f $WORK/server.log ]]; then
    echo "product gate: server log after failure" >&2
    tail -80 "$WORK/server.log" >&2
  fi
  [[ -n $WATCHER_PID ]] && kill "$WATCHER_PID" 2>/dev/null
  [[ -n $SAMPLER_PID ]] && kill "$SAMPLER_PID" 2>/dev/null
  [[ -n $SERVER_PID ]] && kill "$SERVER_PID" 2>/dev/null
  pkill -TERM -P $$ 2>/dev/null
  [[ -n $WATCHER_PID ]] && wait "$WATCHER_PID" 2>/dev/null
  [[ -n $SAMPLER_PID ]] && wait "$SAMPLER_PID" 2>/dev/null
  [[ -n $SERVER_PID ]] && wait "$SERVER_PID" 2>/dev/null
  for _ in $(seq 1 30); do
    drop_own_schema && break
    sleep 1
  done
  if [[ -f $LOW_DISK_MARKER && ! -f $RESULT ]]; then
    python3 -I - "$RESULT" "$REPORT" "$SCHEMA" <<'PY'
import json,sys
result,report,schema=sys.argv[1:]
document={"kind":"supermetry product benchmark","pass":False,"aborted":True,"reason":"low disk","schema":schema,"checks":[]}
open(result,"w",encoding="utf-8").write(json.dumps(document,indent=2)+"\n")
open(report,"w",encoding="utf-8").write("# Supermetry product benchmark\n\nOverall: **ABORTED**\n\nAborted: low disk guard crossed 15 GB; the disposable schema was dropped.\n")
PY
  fi
  find "$WORK" -depth -delete 2>/dev/null
  return "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

watch_disk() {
  local parent_pid=$1
  while sleep 5; do
    available=$(df --output=avail -B1G / | tail -1 | tr -d ' ')
    if (( available < MIN_FREE_GB )); then
      printf 'aborted: low disk (%s GB free; minimum %s GB)\n' "$available" "$MIN_FREE_GB" | tee "$LOW_DISK_MARKER" >&2
      pkill -TERM -P "$parent_pid" 2>/dev/null || true
      drop_own_schema || true
      kill -TERM "$parent_pid" 2>/dev/null || true
      return
    fi
  done
}

available_gb=$(df --output=avail -B1G / | tail -1 | tr -d ' ')
if (( available_gb < MIN_START_GB )); then
  printf 'refusing benchmark load: %s GB free; need at least %s GB\n' "$available_gb" "$MIN_START_GB" >&2
  exit 1
fi
if (( available_gb >= FULL_DATA_GB )); then
  DATASET_SCOPE=full
  DATASET_NOTE="Full Tier S: all 100 sites and 90 million events."
else
  DATASET_SCOPE=site1-only
  DATASET_NOTE="Reduced Tier S: site 1 is complete; non-target sites were omitted because ${available_gb} GB free was below the ${FULL_DATA_GB} GB safe full-load requirement."
fi

echo "product gate: remove leftover schemas owned by $SCHEMA_PREFIX"
while IFS= read -r old_schema; do
  [[ $old_schema =~ ^${SCHEMA_PREFIX}[a-z0-9_]+$ ]] || continue
  env -u PGOPTIONS psql "$BASE_DSN" -v ON_ERROR_STOP=1 -c "drop schema if exists \"$old_schema\" cascade" >/dev/null
done < <(env -u PGOPTIONS psql "$BASE_DSN" -Atqc "select nspname from pg_namespace where nspname like '${SCHEMA_PREFIX}%' order by 1")

echo "product gate: build benchmark tools"
(cd "$ROOT/bench/gen" && go build -o "$WORK/gen" .)
(cd "$ROOT/bench/load" && go build -o "$WORK/loadtest" ./loadtest)
GEN="$WORK/gen"
LOADTEST="$WORK/loadtest"

env -u PGOPTIONS psql "$BASE_DSN" -v ON_ERROR_STOP=1 -c "create schema \"$SCHEMA\"" >/dev/null
(trap - EXIT INT TERM; watch_disk $$) &
WATCHER_PID=$!
export PGOPTIONS="-csearch_path=$SCHEMA"
export SUPERMETRY_DATABASE_URL="$BASE_DSN"
export SUPERMETRY_BASE_URL=http://127.0.0.1:18080
export SUPERMETRY_LISTEN=127.0.0.1:18080
export SUPERMETRY_SECRET_KEY=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
export SUPERMETRY_ADMIN_API_KEY=product-benchmark-key
export SUPERMETRY_TRUSTED_PROXIES=127.0.0.1
export SUPERMETRY_GEOIP_PATH="$ROOT/internal/enrich/lists/dbip-country-lite.mmdb"
export SUPERMETRY_QUERY_TIMEOUT=30s
export SUPERMETRY_FLUSH_EVERY=100ms
export SUPERMETRY_SESSION_FLUSH=500ms

echo "product gate: generate site metadata"
"$GEN" --tier S --days 1 --seed 42 --out /dev/null --sites "$WORK/gen-sites.json" --truth /dev/null --quiet
python3 -I - "$WORK/gen-sites.json" "$WORK/sites.json" "$WORK/loadtest-sites.json" <<'PY'
import json,sys
source,product_path,loadtest_path=sys.argv[1:]
with open(source,encoding="utf-8") as handle:
    sites=json.load(handle)
product=[]
loadtest=[]
for site in sites:
    host=site["host"]
    product.append({"id":site["id"],"org_id":1,"public_id":host,"timezone":site["timezone"],"retention_days":365,"allowed_hosts":[host]})
    loadtest.append({"site":site["id"],"host":host,"id":host})
with open(product_path,"w",encoding="utf-8") as handle:
    json.dump(product,handle,separators=(",",":"))
with open(loadtest_path,"w",encoding="utf-8") as handle:
    json.dump(loadtest,handle,separators=(",",":"))
PY
export SUPERMETRY_SITES_FILE="$WORK/sites.json"

echo "product gate: generate and load Tier S (90 days)"
set +e
if [[ $DATASET_SCOPE == full ]]; then
  "$GEN" --tier S --days 90 --seed 42 --out - --sites "$WORK/gen-sites.json" --truth "$WORK/truth.json" --quiet |
    "$SUPERMETRY" load >"$WORK/load.json" 2>"$WORK/load.log"
  statuses=("${PIPESTATUS[@]}")
else
  "$GEN" --tier S --days 90 --seed 42 --out - --sites "$WORK/gen-sites.json" --truth "$WORK/truth.json" --quiet |
    awk 'index($0,"\"site\":1,")' |
    "$SUPERMETRY" load >"$WORK/load.json" 2>"$WORK/load.log"
  pipeline_statuses=("${PIPESTATUS[@]}")
  statuses=("${pipeline_statuses[0]}" "${pipeline_statuses[2]}")
fi
set -e
if (( statuses[0] != 0 || statuses[1] != 0 )); then
  tail -40 "$WORK/load.log" >&2
  exit 1
fi

echo "product gate: close site 1 days"
"$SUPERMETRY" close-days --site 1 --from 2026-07-03 --to 2026-09-30 >"$WORK/close.log" 2>&1
psql "$BASE_DSN" -v ON_ERROR_STOP=1 -c 'analyze sessions_closed, custom_closed, cube_day, visitor_day, rollup_day, rollup_day_dim, rollup_month_dim, rollup_block_dim, day_close, dict' >/dev/null

echo "product gate: start supermetry serve"
export GOMEMLIMIT=40MiB
export GOGC=50
start_ns=$(date +%s%N)
"$SUPERMETRY" serve >"$WORK/server.log" 2>&1 &
SERVER_PID=$!
ready=false
for _ in $(seq 1 2400); do
  if curl -fsS http://127.0.0.1:18080/readyz >/dev/null 2>&1; then
    ready=true
    break
  fi
  kill -0 "$SERVER_PID" 2>/dev/null || { tail -80 "$WORK/server.log" >&2; exit 1; }
  sleep 0.05
done
[[ $ready == true ]] || { echo "server did not become ready in 120 seconds" >&2; exit 1; }
end_ns=$(date +%s%N)
python3 -I - "$start_ns" "$end_ns" >"$WORK/startup-ms" <<'PY'
import sys
print((int(sys.argv[2])-int(sys.argv[1]))/1_000_000)
PY
sleep 5
awk '/VmRSS:/ {print $2}' "/proc/$SERVER_PID/status" >"$WORK/r1-kb"
curl -fsS http://127.0.0.1:18080/js/script.js >"$WORK/tracker.js"
python3 -I - "$WORK/tracker.js" >"$WORK/tracker-gzip-bytes" <<'PY'
import gzip,sys
print(len(gzip.compress(open(sys.argv[1],"rb").read(),compresslevel=9,mtime=0)))
PY

echo "product gate: dashboard, filtered queries, funnel, property breakdown, and C1"
python3 -I "$ROOT/bench/product/measure.py" --key product-benchmark-key --truth "$WORK/truth.json" --out "$WORK/query.json"

echo "product gate: R2 at 1,000 events/s for 60 seconds"
(
  while kill -0 "$SERVER_PID" 2>/dev/null; do
    awk '/VmRSS:/ {print $2}' "/proc/$SERVER_PID/status"
    sleep 1
  done
) >"$WORK/r2-rss-kb" &
SAMPLER_PID=$!
set +e
"$GEN" --tier S --days 1 --start 2026-10-01 --seed 42 --out - --sites /dev/null --truth /dev/null --quiet |
  "$LOADTEST" -product supermetry -url http://127.0.0.1:18080 -sites "$WORK/loadtest-sites.json" -rate 1000 -duration 60s -concurrency 256 -timeout 10s -label product-r2 -out "$WORK/r2-loadtest.json"
statuses=("${PIPESTATUS[@]}")
set -e
kill "$SAMPLER_PID" 2>/dev/null || true
wait "$SAMPLER_PID" 2>/dev/null || true
SAMPLER_PID=
(( statuses[1] == 0 )) || { echo "R2 load test failed" >&2; exit 1; }

echo "product gate: S1 at 10,000 events/s for 60 seconds"
set +e
"$GEN" --tier S --days 1 --start 2026-10-02 --seed 42 --out - --sites /dev/null --truth /dev/null --quiet |
  "$LOADTEST" -product supermetry -url http://127.0.0.1:18080 -sites "$WORK/loadtest-sites.json" -rate 10000 -duration 60s -concurrency 1024 -timeout 10s -label product-s1 -out "$WORK/s1-loadtest.json"
statuses=("${PIPESTATUS[@]}")
set -e
(( statuses[1] == 0 )) || { echo "S1 load test failed" >&2; exit 1; }

python3 -I - "$WORK/query.json" "$WORK/r1-kb" "$WORK/r2-rss-kb" "$WORK/s1-loadtest.json" "$SUPERMETRY" "$WORK/tracker-gzip-bytes" "$WORK/startup-ms" "$WORK/measurements.json" "$WORK/load.json" "$WORK/r2-loadtest.json" "$DATASET_SCOPE" "$DATASET_NOTE" <<'PY'
import json,os,platform,sys
query_path,r1_path,r2_path,s1_path,supermetry_path,tracker_path,startup_path,out_path,load_path,r2_load_path,dataset_scope,dataset_note=sys.argv[1:]
query=json.load(open(query_path,encoding="utf-8"))
timings=query["timings"]
s1=json.load(open(s1_path,encoding="utf-8"))
r2=json.load(open(r2_load_path,encoding="utf-8"))
duration=60
measured={
 "startup_ms":float(open(startup_path).read()),
 "S1":{"ok_rate":s1["ok_2xx"]/duration,"p99_ms":s1["latency_from_schedule"]["p99_ms"],"sent":s1["sent"],"ok_2xx":s1["ok_2xx"],"client_overflow":s1["client_overflow"],"codes":s1["codes"]},
 "dashboard_p95_ms":{"S30":timings["DASHBOARD_S30"]["p95_ms"],"S90":timings["DASHBOARD_S90"]["p95_ms"]},
 "S2_p95_ms":{"S30":timings["S2_S30"]["p95_ms"],"S90":timings["S2_S90"]["p95_ms"]},
 "S3_p95_ms":{"S30":[timings[f"S3_S30_{name}"]["p95_ms"] for name in ("country","country_source","country_path")],"S90":[timings[f"S3_S90_{name}"]["p95_ms"] for name in ("country","country_source","country_path")]},
 "S4_p95_ms":{"S30":timings["S4_S30"]["p95_ms"]},
 "F4_p95_ms":{"S30":timings["F4_S30"]["p95_ms"]},
 "R1_mb":int(open(r1_path).read().strip())/1024,
 "R2_mb":max(int(line) for line in open(r2_path) if line.strip())/1024,
 "binary_mb":os.path.getsize(supermetry_path)/(1024*1024),
 "tracker_gzip_bytes":int(open(tracker_path).read()),
 "C1_mismatches":query["c1"]["mismatches"],
}
document={"kind":"supermetry product benchmark","tier":"S","days":90,"seed":42,"dataset_scope":dataset_scope,"dataset_note":dataset_note,"measured":measured,"query_details":timings,"c1":query["c1"],"load":json.load(open(load_path)),"r2_loadtest":r2,"s1_loadtest":s1,"rig":{"kernel":platform.release(),"machine":platform.machine(),"python":platform.python_version()}}
json.dump(document,open(out_path,"w",encoding="utf-8"),indent=2)
PY

set +e
python3 -I "$ROOT/bench/product/evaluate.py" --measurements "$WORK/measurements.json" --thresholds "$THRESHOLDS" --json "$RESULT" --markdown "$REPORT"
gate_status=$?
set -e

if (( gate_status != 0 )); then
  echo "product gate: capture failure evidence"
  failed=$(python3 -I - "$RESULT" <<'PY'
import json,sys
print(" ".join(item["id"] for item in json.load(open(sys.argv[1]))["checks"] if not item["pass"]))
PY
)
  declare -a artifacts=()
  if [[ $failed == *S2* || $failed == *S3* || $failed == *S4* || $failed == *F4* || $failed == *DASHBOARD* || $failed == *REG-* ]]; then
    for name in country country-source country-path; do
      artifact="$RESULT_BASE-explain-$name.txt"
      case $name in
        country) predicate="s.country='US'" ;;
        country-source) predicate="s.country='US' and s.source_id=(select id from dict where kind=2 and value='(direct)' limit 1)" ;;
        country-path) predicate="s.country='US' and exists (select 1 from unnest(s.seq) code where code>0 and (code >> 2)=(select id from dict where kind=0 and value='/' limit 1))" ;;
      esac
      psql "$BASE_DSN" -X -v ON_ERROR_STOP=1 -c "explain (analyze, buffers, wal, settings, summary) select count(distinct (s.day,s.visitor_id)),count(*),coalesce(sum(s.pageviews),0),count(*) filter (where s.pageviews=1 and s.custom=0),coalesce(sum(s.duration_ms),0) from sessions_closed s where s.site_id=1 and s.day between date '2026-07-03' and date '2026-09-30' and $predicate" >"$artifact" 2>&1 || true
      artifacts+=("$(basename "$artifact")")
    done
  fi
  if [[ $failed == *R1* || $failed == *R2* ]]; then
    pmap -x "$SERVER_PID" >"$RESULT_BASE-memory-map.txt" 2>&1 || true
    cp "/proc/$SERVER_PID/smaps" "$RESULT_BASE-heap-map.txt" 2>/dev/null || true
    artifacts+=("$(basename "$RESULT_BASE-memory-map.txt")" "$(basename "$RESULT_BASE-heap-map.txt")")
    kill -QUIT "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    SERVER_PID=
    cp "$WORK/server.log" "$RESULT_BASE-goroutines.txt"
    artifacts+=("$(basename "$RESULT_BASE-goroutines.txt")")
  fi
  python3 -I - "$WORK/measurements.json" "$failed" "${artifacts[*]}" <<'PY'
import json,sys
path,failed,artifacts=sys.argv[1:]
document=json.load(open(path,encoding="utf-8"))
names=artifacts.split()
document["diagnostics"]={metric:names for metric in failed.split()}
json.dump(document,open(path,"w",encoding="utf-8"),indent=2)
PY
  set +e
  python3 -I "$ROOT/bench/product/evaluate.py" --measurements "$WORK/measurements.json" --thresholds "$THRESHOLDS" --json "$RESULT" --markdown "$REPORT"
  gate_status=$?
  set -e
fi

cat "$REPORT"
exit "$gate_status"
