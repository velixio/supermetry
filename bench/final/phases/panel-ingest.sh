#!/usr/bin/env bash
set -euo pipefail
PHASE=panel-ingest; source "$(dirname "$0")/../lib.sh"
say "ETA: 30-35 minutes"
ART="$RESULTS_DIR/artifacts/panel-ingest"
PROTO="$ROOT/bench/proto/proto"; SITES="$RESULTS_DIR/artifacts/panel-s/sites.json"
[[ -f $SITES ]] || SITES="$ROOT/bench/load/data/sites.json"
plan "start proto :8090; S1 HTTP steps 5K/10K/20K/40K events/s for 60s each"
plan "R1 idle 5m and R2 1K events/s 10m; sample app RSS, sum postgres PSS via sudo -n, whole stack"
plan "S5 probe: realtime visibility <=5s and dashboard visibility <=60s"
(( DRY_RUN )) && dry_finish
fresh_artifacts "$ART"
require curl; sudo -n true; [[ -x $PROTO ]] || exit 1
pid=; cleanup(){ [[ -z ${pid:-} ]] || { kill -TERM "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; }; }; trap cleanup EXIT
"$PROTO" serve --sites "$SITES" --geoip "$ROOT/bench/proto/data/dbip-country-lite.mmdb" >"$ART/server.log" 2>&1 & pid=$!
for _ in {1..120}; do curl -fsS http://127.0.0.1:8090/api/stats >/dev/null 2>&1 && break; sleep .25; done
kill -0 "$pid"
"$FINAL_DIR/freshness.py" --url http://127.0.0.1:8090 --out "$ART/s5.json"
"$FINAL_DIR/memory-sample.sh" "$pid" 300 10 "$ART/r1-memory.jsonl"
for rate in 5000 10000 20000 40000; do
  quiet_gate
  "$PROTO" loadtest --rate "$rate" --duration 60s --churn --sites "$SITES" --out "$ART" --label "final-s1-$rate-noisy=$NOISY"
  sleep 15
done
quiet_gate
"$FINAL_DIR/memory-sample.sh" "$pid" 600 10 "$ART/r2-memory.jsonl" & mp=$!
"$PROTO" loadtest --rate 1000 --duration 10m --churn --sites "$SITES" --out "$ART" --label "final-r2-noisy=$NOISY"
wait "$mp"
python3 -I - "$ART/meta.json" "$ART" <<'PY'
import glob,json,os,statistics,sys
d=sys.argv[2]
def mem(name):
 rows=[json.loads(x) for x in open(os.path.join(d,name))]
 return {k:{'p50':statistics.median(r[k] for r in rows),'max':max(r[k] for r in rows)} for k in ('app_rss_bytes','postgres_pss_bytes','stack_bytes')}
files=glob.glob(d+'/proto-loadtest-*.json')
json.dump({'r1':mem('r1-memory.jsonl'),'r2':mem('r2-memory.jsonl'),'loadtests':[os.path.basename(x) for x in files]},open(sys.argv[1],'w'),indent=2)
PY
cleanup; pid=; trap - EXIT
finish complete "$ART/meta.json"
