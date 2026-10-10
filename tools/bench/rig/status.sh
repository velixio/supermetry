#!/usr/bin/env bash
# Show state of plausible|umami|goatcounter|all. Exit 0 only if every requested product is up.
set -uo pipefail
. "$(dirname "$0")/lib.sh"
rc=0
nsites() { [ -f "$RIG/$1-sites.json" ] && grep -c '"site"' "$RIG/$1-sites.json" || echo 0; }
row() { printf '%-12s %-5s %-6s %s\n' "$@"; }
row PRODUCT STATE PORT DETAIL

st_plausible() {
  local n health
  n=$(pcompose ps --status running -q 2>/dev/null | wc -l)
  health=$(curl -fsS --max-time 3 "http://127.0.0.1:$PLAUSIBLE_PORT/api/system/health/ready" 2>/dev/null | tr -d '\n ' || true)
  if [ "$n" -ge 3 ] && [ -n "$health" ]; then
    row plausible up ":$PLAUSIBLE_PORT" "containers=$n/3 sites=$(nsites plausible) $PLAUSIBLE_IMAGE health=$health"
  else row plausible down ":$PLAUSIBLE_PORT" "containers=$n/3"; rc=1; fi
}

st_umami() {
  if curl -fsS --max-time 3 "$(umami_url)/api/heartbeat" >/dev/null 2>&1; then
    row umami up ":$UMAMI_PORT" "container=$UMAMI_CT (+ local PG db umami) sites=$(nsites umami) $UMAMI_IMAGE"
  else row umami down ":$UMAMI_PORT" "$("${D[@]}" ps -a --filter "name=^$UMAMI_CT\$" --format '{{.Status}}' 2>/dev/null)"; rc=1; fi
}

st_goatcounter() {
  if gc_running && curl -fsS --max-time 3 "http://127.0.0.1:$GOATCOUNTER_PORT/status" >/dev/null 2>&1; then
    row goatcounter up ":$GOATCOUNTER_PORT" "pid=$(cat "$GC_PID") (+ local PG db goatcounter) sites=$(nsites goatcounter) $GOATCOUNTER_VERSION"
  else row goatcounter down ":$GOATCOUNTER_PORT" "$( [ -x "$GC_BIN" ] && echo "binary present" || echo "not installed")"; rc=1; fi
}

for p in $(products "${1:-}"); do "st_$p"; done
exit $rc
