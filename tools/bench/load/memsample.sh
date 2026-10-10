#!/usr/bin/env bash
# Sample a product's memory every INTERVAL s for SECS s (R2), one JSON object per line.
# Usage: memsample.sh umami|plausible|goatcounter SECS INTERVAL OUT.jsonl
# Uses tools/bench/rig/memstat.py (as root) for /proc RSS/PSS/anon, like R1 (r1-idle.sh):
#   app      the product's own processes (container processes, or the goatcounter binary)
#   pg       local-PG client backends of its database (umami/goatcounter)
#   docker   `docker stats` MemUsage per container (cgroup, excl. inactive file cache)
set -euo pipefail
LOAD="$(cd "$(dirname "$0")" && pwd)"
. "$LOAD/../rig/lib.sh"
P=$1 SECS=$2 IV=$3 OUT=$4
: >"$OUT"
end=$(( $(date +%s) + SECS ))
while [ "$(date +%s)" -lt "$end" ]; do
  t=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  case $P in
    umami)
      app=$("${D[@]}" top bench-umami -eo pid | tail -n +2 | paste -sd, -)
      pg=$(pg_admin -c "SELECT pid FROM pg_stat_activity WHERE datname='umami' AND backend_type='client backend'" | paste -sd, -)
      cts="bench-umami" ;;
    plausible)
      app=""; pg=""
      cts=$("${D[@]}" ps --format '{{.Names}}' | grep '^plausible-' | paste -sd' ' -) ;;
    goatcounter)
      app=$(cat "$LOAD/data/goatcounter.pid" 2>/dev/null || cat "$GC_PID")
      pg=$(pg_admin -c "SELECT pid FROM pg_stat_activity WHERE datname='goatcounter' AND backend_type='client backend'" | paste -sd, -)
      cts="" ;;
  esac
  pgsrv=$(pg_admin -c "SELECT pid FROM pg_stat_activity WHERE backend_type <> 'client backend'" | paste -sd, -)
  pgsrv="$pgsrv,$(head -1 /var/run/postgresql/18-main.pid 2>/dev/null || true)"
  mem=$(printf '{"app":[%s],"pg":[%s],"pg_server":[%s]}' "$app" "$pg" "${pgsrv#,}" | sudo python3 -I "$RIG/memstat.py")
  ds="{}"
  if [ -n "$cts" ]; then
    ds=$("${D[@]}" stats --no-stream --format '{{.Name}}\t{{.MemUsage}}' $cts | python3 -I -c '
import sys, json
u = {"KiB": 1/1024, "MiB": 1, "GiB": 1024, "B": 1/1048576, "kB": 1/1024, "MB": 1, "GB": 1024}
out = {}
for line in sys.stdin:
    name, mu = line.rstrip("\n").split("\t")
    v = mu.split("/")[0].strip()
    for k in sorted(u, key=len, reverse=True):
        if v.endswith(k):
            out[name] = round(float(v[: -len(k)]) * u[k], 1); break
print(json.dumps(out))')
  fi
  printf '{"t":"%s","mem":%s,"docker_mib":%s}\n' "$t" "$mem" "$ds" >>"$OUT"
  sleep "$IV"
done
