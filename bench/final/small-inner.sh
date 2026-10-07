#!/usr/bin/env bash
set -euo pipefail
[[ $# == 2 ]] || { echo 'Usage: small-inner.sh RESULTS_DIR CLUSTER' >&2; exit 2; }
out=$1 cluster=$2; root=$(cd "$(dirname "$0")/../.." && pwd)
mkdir -p "$out"; pgpid= apppid=
cleanup(){ [[ -z ${apppid:-} ]] || kill -TERM "$apppid" 2>/dev/null || true; [[ -z ${pgpid:-} ]] || kill -TERM "$pgpid" 2>/dev/null || true; wait 2>/dev/null || true; }
trap cleanup EXIT
pg_ctlcluster 18 "$cluster" start --foreground >"$out/postgres.log" 2>&1 & pgpid=$!
for _ in {1..120}; do pg_isready -p 5433 >/dev/null 2>&1 && break; sleep .25; done
runuser -u postgres -- psql -p 5433 -v ON_ERROR_STOP=1 <<'SQL'
CREATE ROLE panel LOGIN PASSWORD 'panel';
CREATE DATABASE panel OWNER panel;
SQL
dsn='postgresql://panel:panel@127.0.0.1:5433/panel?sslmode=disable'
"$root/bench/load/bin/gen" --tier S --days 1 --seed 42 --out /dev/null --sites "$out/sites.json" --truth /dev/null --quiet  # pre-generate sites.json (race fix)
"$root/bench/load/bin/gen" --tier S --days 30 --seed 42 --out - --sites "$out/sites.json" --truth "$out/truth.json" --quiet |
  grep -F '"site":1,' | PANEL_DSN="$dsn" "$root/bench/proto/proto" load --sites "$out/sites.json"
PANEL_DSN="$dsn" "$root/bench/proto/proto" rollup --from 2026-09-01 --to 2026-09-30 --parallel 2 --drop-hot
PANEL_DSN="$dsn" "$root/bench/proto/proto" serve --addr=127.0.0.1:18095 --sites "$out/sites.json" --geoip='' >"$out/app.log" 2>&1 & apppid=$!
sleep 1
PANEL_DSN="$dsn" "$root/bench/proto/proto" bench --tier S-site1 --site 1 --to 2026-09-30 --s2-days 30 --s3-days 30 --cases s2,s3 --warmup 3 --runs 30 --kind final-panel-small --out "$out"
cg=$(awk -F: '$1=="0"{print $3}' /proc/self/cgroup)
base="/sys/fs/cgroup$cg"
python3 -I - "$out/cgroup.json" "$base" <<'PY'
import json,os,sys
b=sys.argv[2]
def read(n):
 try:return open(os.path.join(b,n)).read().strip()
 except OSError:return 'unavailable'
json.dump({'path':b,'memory.current':read('memory.current'),'memory.peak':read('memory.peak'),'memory.events':read('memory.events'),'cpu.max':read('cpu.max')},open(sys.argv[1],'w'),indent=2)
PY

