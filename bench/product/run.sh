#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: bench/product/run.sh tier-s" >&2
  exit 2
}

[[ ${1:-} == tier-s && $# == 1 ]] || usage

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
PANEL="$ROOT/bin/panel"
GEN="$ROOT/bench/load/bin/gen"
LOADTEST="$ROOT/bench/load/bin/loadtest"
THRESHOLDS="$ROOT/bench/product/thresholds.json"
BASE_DSN=${PANEL_TEST_DSN:-postgres://panel:panel@127.0.0.1:5432/panel?sslmode=disable}
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
RESULT="$ROOT/bench/results/product-$STAMP.json"
WORK=$(mktemp -d -t panel-product.XXXXXX)
SCHEMA="product_$(printf '%s' "$STAMP" | tr '[:upper:]' '[:lower:]' | tr -c '0-9a-z' '_')_$$"
SERVER_PID=
PROXY_PID=
SAMPLER_PID=

for command in python3 curl psql; do
  command -v "$command" >/dev/null || { echo "missing command: $command" >&2; exit 1; }
done
[[ -x $PANEL ]] || { echo "build bin/panel first with make build" >&2; exit 1; }
[[ -x $GEN && -x $LOADTEST ]] || { echo "missing bench/load/bin/gen or loadtest" >&2; exit 1; }

cleanup() {
	status=$?
	set +e
	if (( status != 0 )) && [[ -f $WORK/server.log ]]; then
	  echo "product gate: server log after failure" >&2
	  tail -80 "$WORK/server.log" >&2
	fi
  [[ -n $SAMPLER_PID ]] && kill "$SAMPLER_PID" 2>/dev/null
  [[ -n $PROXY_PID ]] && kill "$PROXY_PID" 2>/dev/null
  [[ -n $SERVER_PID ]] && kill "$SERVER_PID" 2>/dev/null
  [[ -n $SAMPLER_PID ]] && wait "$SAMPLER_PID" 2>/dev/null
  [[ -n $PROXY_PID ]] && wait "$PROXY_PID" 2>/dev/null
  [[ -n $SERVER_PID ]] && wait "$SERVER_PID" 2>/dev/null
  env -u PGOPTIONS psql "$BASE_DSN" -v ON_ERROR_STOP=1 -c "drop schema if exists \"$SCHEMA\" cascade" >/dev/null 2>&1
  rm -rf -- "$WORK"
}
trap cleanup EXIT INT TERM

env -u PGOPTIONS psql "$BASE_DSN" -v ON_ERROR_STOP=1 -c "create schema \"$SCHEMA\"" >/dev/null
export PGOPTIONS="-csearch_path=$SCHEMA"
export PANEL_DATABASE_URL="$BASE_DSN"
export PANEL_BASE_URL=http://127.0.0.1:18080
export PANEL_LISTEN=127.0.0.1:18080
export PANEL_SECRET_KEY=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
export PANEL_ADMIN_API_KEY=product-benchmark-key
export PANEL_TRUSTED_PROXIES=127.0.0.1
export PANEL_GEOIP_PATH="$ROOT/bench/proto/data/dbip-country-lite.mmdb"
export PANEL_QUERY_TIMEOUT=30s
export PANEL_FLUSH_EVERY=100ms
export PANEL_SESSION_FLUSH=500ms

echo "product gate: generate site metadata"
"$GEN" --tier S --days 1 --seed 42 --out /dev/null --sites "$WORK/gen-sites.json" --truth /dev/null --quiet
python3 -I - "$WORK/gen-sites.json" "$WORK/sites.json" "$WORK/loadtest-sites.json" <<'PY'
import json,sys
source,product_path,loadtest_path=sys.argv[1:]
sites=json.load(open(source,encoding="utf-8"))
product=[]
loadtest=[]
for site in sites:
    host=site["host"]
    product.append({"id":site["id"],"org_id":1,"public_id":host,"timezone":site["timezone"],"retention_days":365,"allowed_hosts":[host]})
    loadtest.append({"site":site["id"],"host":host,"id":host})
json.dump(product,open(product_path,"w",encoding="utf-8"),separators=(",",":"))
json.dump(loadtest,open(loadtest_path,"w",encoding="utf-8"),separators=(",",":"))
PY
export PANEL_SITES_FILE="$WORK/sites.json"

echo "product gate: generate and load Tier S (90 days)"
set +e
"$GEN" --tier S --days 90 --seed 42 --out - --sites "$WORK/gen-sites.json" --truth "$WORK/truth.json" --quiet |
  "$PANEL" load >"$WORK/load.json" 2>"$WORK/load.log"
load_status=${PIPESTATUS[1]}
set -e
if (( load_status != 0 )); then
  tail -40 "$WORK/load.log" >&2
  exit "$load_status"
fi

echo "product gate: close site 1 days"
"$PANEL" close-days --site 1 --from 2026-07-03 --to 2026-09-30 >"$WORK/close.log" 2>&1
psql "$BASE_DSN" -v ON_ERROR_STOP=1 -c 'analyze sessions_closed, custom_closed, cube_day, visitor_day, rollup_day, rollup_day_dim, rollup_month_dim, rollup_block_dim, day_close, dict' >/dev/null

echo "product gate: start panel serve"
export GOMEMLIMIT=40MiB
export GOGC=50
"$PANEL" serve >"$WORK/server.log" 2>&1 &
SERVER_PID=$!
for _ in $(seq 1 600); do
  if curl -fsS http://127.0.0.1:18080/readyz >/dev/null 2>&1; then
    break
  fi
  kill -0 "$SERVER_PID" 2>/dev/null || { tail -80 "$WORK/server.log" >&2; exit 1; }
  sleep 1
done
curl -fsS http://127.0.0.1:18080/readyz >/dev/null
sleep 5
awk '/VmRSS:/ {print $2}' "/proc/$SERVER_PID/status" >"$WORK/r1-kb"

cat >"$WORK/adapter.py" <<'PY'
import http.server,json,urllib.request,urllib.error
TARGET="http://127.0.0.1:18080/api/public/v1/events"
class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self,*args): pass
    def do_POST(self):
        try:
            size=int(self.headers.get("Content-Length","0"))
            raw=json.loads(self.rfile.read(size))
            body={"site":raw.get("d",""),"name":raw.get("n",""),"url":raw.get("u",""),"referrer":raw.get("r","") or "","screen_width":raw.get("w",0),"props":raw.get("p",{}) or {}}
            request=urllib.request.Request(TARGET,data=json.dumps(body,separators=(",",":")).encode(),method="POST",headers={"Content-Type":"application/json","User-Agent":self.headers.get("User-Agent","") or "benchmark","X-Forwarded-For":self.headers.get("X-Forwarded-For","")})
            try:
                with urllib.request.urlopen(request,timeout=10) as response:
                    status=response.status
                    payload=response.read()
            except urllib.error.HTTPError as error:
                status=error.code
                payload=error.read()
            self.send_response(status)
            self.end_headers()
            self.wfile.write(payload)
        except Exception:
            self.send_response(503)
            self.end_headers()
http.server.ThreadingHTTPServer(("127.0.0.1",18081),Handler).serve_forever()
PY
python3 -I "$WORK/adapter.py" &
PROXY_PID=$!
for _ in $(seq 1 40); do
  kill -0 "$PROXY_PID" 2>/dev/null || exit 1
  if python3 -I - <<'PY' >/dev/null 2>&1
import socket
s=socket.create_connection(("127.0.0.1",18081),0.2);s.close()
PY
  then break; fi
  sleep 0.1
done

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
  "$LOADTEST" -product plausible -url http://127.0.0.1:18081 -sites "$WORK/loadtest-sites.json" -rate 1000 -duration 60s -concurrency 256 -timeout 10s -label product-r2 -out "$WORK/loadtest.json"
statuses=("${PIPESTATUS[@]}")
set -e
kill "$SAMPLER_PID" 2>/dev/null || true
wait "$SAMPLER_PID" 2>/dev/null || true
SAMPLER_PID=
if (( statuses[1] != 0 )); then
  echo "loadtest failed with status ${statuses[1]}" >&2
  exit "${statuses[1]}"
fi

echo "product gate: query timings and C1"
python3 -I - "$WORK/query.json" "$WORK/truth.json" <<'PY'
import json,math,sys,time,urllib.request,urllib.error
out,truth_path=sys.argv[1:]
base="http://127.0.0.1:18080"
headers={"Authorization":"Bearer product-benchmark-key","Content-Type":"application/json"}
def post(path,body):
    request=urllib.request.Request(base+path,data=json.dumps(body,separators=(",",":")).encode(),headers=headers,method="POST")
    started=time.perf_counter()
    try:
        with urllib.request.urlopen(request,timeout=30) as response:
            payload=response.read()
            status=response.status
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"HTTP {error.code}: {error.read(200)!r}") from None
    elapsed=(time.perf_counter()-started)*1000
    if status != 200:
        raise RuntimeError(f"HTTP {status}")
    time.sleep(0.51)
    return elapsed,json.loads(payload)
def query(days,filters=None,dimensions=None,metrics=None):
    start="2026-09-01" if days==30 else "2026-07-03"
    body={"site_id":1,"date_range":{"from":start,"to":"2026-09-30"},"metrics":metrics or ["visitors","visits","pageviews","bounce_rate","visit_duration"]}
    if filters: body["filters"]=filters
    if dimensions: body["dimensions"]=dimensions
    return "/api/public/v1/query",body
def filt(dimension,value): return {"dimension":dimension,"operator":"is","values":[value]}
cases={}
for days in (30,90):
    cases[f"S2_S{days}"]=query(days)
    cases[f"S3_S{days}_country"]=query(days,[filt("country","US")])
    cases[f"S3_S{days}_country_source"]=query(days,[filt("country","US"),filt("source","(direct)")])
    cases[f"S3_S{days}_country_path"]=query(days,[filt("country","US"),filt("path","/")])
cases["S4_S30"]=("/api/public/v1/funnels/query",{"site_id":1,"date_range":{"from":"2026-09-01","to":"2026-09-30"},"steps":[{"type":"page","match":"exact","value":"/"},{"type":"page","match":"exact","value":"/pricing"},{"type":"event","match":"exact","value":"signup"}]})
cases["F4_S30"]=query(30,[filt("event","signup"),filt("country","US")],["prop:plan"],["events"])
timings={}
responses={}
for name,(path,body) in cases.items():
    samples=[]
    for iteration in range(33):
        elapsed,response=post(path,body)
        if iteration >= 3: samples.append(elapsed)
    samples.sort()
    timings[name]={"p50_ms":samples[14],"p95_ms":samples[math.ceil(len(samples)*.95)-1],"max_ms":samples[-1]}
    responses[name]=response
truth=json.load(open(truth_path,encoding="utf-8"))
expected={"visitors":0,"visits":0,"pageviews":0,"bounces":0,"duration_sum_s":0.0}
for row in truth["rows"]:
    if row["site"]==1 and "2026-09-01" <= row["date"] <= "2026-09-30":
        for key in expected: expected[key]+=row[key]
_,actual_response=post(*query(30))
actual=actual_response["totals"]
checks={
    "visitors":round(actual["visitors"])==expected["visitors"],
    "visits":round(actual["visits"])==expected["visits"],
    "pageviews":round(actual["pageviews"])==expected["pageviews"],
    "bounces":round(actual["bounce_rate"]*expected["visits"]/100)==expected["bounces"],
    "duration":abs(actual["visit_duration"]*expected["visits"]-expected["duration_sum_s"]) <= max(expected["duration_sum_s"]*.01,.001),
}
json.dump({"timings":timings,"c1":{"mismatches":sum(not value for value in checks.values()),"checks":checks,"expected":expected,"actual":actual}},open(out,"w",encoding="utf-8"),indent=2)
PY

python3 -I - "$THRESHOLDS" "$WORK/query.json" "$WORK/r1-kb" "$WORK/r2-rss-kb" "$WORK/load.json" "$WORK/loadtest.json" "$RESULT" <<'PY'
import json,sys
threshold_path,query_path,r1_path,r2_path,load_path,loadtest_path,result_path=sys.argv[1:]
thresholds=json.load(open(threshold_path))
query=json.load(open(query_path))
timings=query["timings"]
measured={
 "S2_p95_ms":{"S30":timings["S2_S30"]["p95_ms"],"S90":timings["S2_S90"]["p95_ms"]},
 "S3_p95_ms":{"S30":[timings[f"S3_S30_{name}"]["p95_ms"] for name in ("country","country_source","country_path")],"S90":[timings[f"S3_S90_{name}"]["p95_ms"] for name in ("country","country_source","country_path")]},
 "S4_p95_ms":{"S30":timings["S4_S30"]["p95_ms"]},
 "F4_p95_ms":{"S30":timings["F4_S30"]["p95_ms"]},
 "R1_mb":int(open(r1_path).read().strip())/1024,
 "R2_mb":max(int(line) for line in open(r2_path) if line.strip())/1024,
 "C1_mismatches":query["c1"]["mismatches"],
}
failures=[]
def must(condition,message):
    if not condition: failures.append(message)
must(max(measured["S2_p95_ms"].values()) < 300,"S2 MUST: p95 must be below 300 ms")
must(max(measured["S3_p95_ms"]["S90"]) < 1000,"S3 MUST: S90 p95 must be below 1000 ms")
must(measured["S4_p95_ms"]["S30"] < 2000,"S4 MUST: p95 must be below 2000 ms")
must(measured["F4_p95_ms"]["S30"] < 1000,"F4 MUST: p95 must be below 1000 ms")
must(measured["R1_mb"] < 50,"R1 MUST: RSS must be below 50 MB")
must(measured["R2_mb"] < 150,"R2 MUST: RSS must be below 150 MB")
must(measured["C1_mismatches"] == 0,"C1 MUST: exact metrics differ from truth")
tolerance=1+thresholds["regression_tolerance"]
for metric in ("S2_p95_ms","S3_p95_ms","S4_p95_ms","F4_p95_ms"):
    for tier,baseline in thresholds[metric].items():
        actual=measured[metric][tier]
        actuals=actual if isinstance(actual,list) else [actual]
        baselines=baseline if isinstance(baseline,list) else [baseline]
        for index,(got,want) in enumerate(zip(actuals,baselines)):
            must(got <= want*tolerance,f"{metric}.{tier}[{index}] regressed more than 20%: {got:.1f} > {want*tolerance:.1f}")
load=json.load(open(load_path))
loadtest=json.load(open(loadtest_path))
result={"kind":"panel product scorecard regression gate","tier":"S","days":90,"seed":42,"measured":measured,"thresholds":thresholds,"c1":query["c1"],"query_details":timings,"load":load,"r2_loadtest":{"target_rate":loadtest.get("target_rate"),"ok_2xx":loadtest.get("ok_2xx"),"sent":loadtest.get("sent"),"codes":loadtest.get("codes")},"pass":not failures,"failures":failures}
json.dump(result,open(result_path,"w",encoding="utf-8"),indent=2)
print(json.dumps({"result":result_path,"pass":result["pass"],"measured":measured,"failures":failures},indent=2))
raise SystemExit(0 if result["pass"] else 1)
PY
