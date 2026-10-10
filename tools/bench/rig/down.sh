#!/usr/bin/env bash
# Stop plausible|umami|goatcounter|all.
#   --purge  also delete all data (compose volumes / local PG db+role) and the site mapping
#   --cold   --purge plus remove the install artefacts (images, CE clone, binary), for a clean O2 run
set -euo pipefail
. "$(dirname "$0")/lib.sh"
target=${1:-}; shift || true
PURGE=0 COLD=0
for a in "$@"; do case $a in --purge) PURGE=1 ;; --cold) PURGE=1 COLD=1 ;; *) die "unknown flag $a" ;; esac; done

down_plausible() {
  RIG_PRODUCT=plausible
  if [ -f "$PCE/compose.yml" ]; then
    log "compose down$([ $PURGE = 1 ] && echo ' -v')"
    # `down` (not `stop`): restart: always would otherwise bring them back on the next dockerd start.
    if [ $PURGE = 1 ]; then pcompose down -v --remove-orphans >&2; else pcompose down --remove-orphans >&2; fi
  fi
  [ $PURGE = 1 ] && rm -f "$RIG/plausible-sites.json"
  if [ $COLD = 1 ]; then
    log "removing images and CE clone"
    "${D[@]}" rmi -f "$PLAUSIBLE_IMAGE" postgres:16-alpine clickhouse/clickhouse-server:24.12-alpine >/dev/null 2>&1 || true
    rm -rf "$PCE"
  fi
  return 0
}

down_umami() {
  RIG_PRODUCT=umami
  log "removing container $UMAMI_CT"
  "${D[@]}" rm -f "$UMAMI_CT" >/dev/null 2>&1 || true
  if [ $PURGE = 1 ]; then log "dropping local PG db+role umami"; drop_pg_db umami; rm -f "$RIG/umami-sites.json" "$RIG/run/umami.env"; fi
  if [ $COLD = 1 ]; then "${D[@]}" rmi -f "$UMAMI_IMAGE" >/dev/null 2>&1 || true; fi
  return 0
}

down_goatcounter() {
  RIG_PRODUCT=goatcounter
  if gc_running; then
    local pid; pid=$(cat "$GC_PID")
    log "stopping pid $pid"
    kill "$pid"
    for _ in $(seq 50); do kill -0 "$pid" 2>/dev/null || break; sleep 0.2; done
    kill -0 "$pid" 2>/dev/null && kill -9 "$pid"
  fi
  rm -f "$GC_PID"
  if [ $PURGE = 1 ]; then log "dropping local PG db+role goatcounter"; drop_pg_db goatcounter; rm -f "$RIG/goatcounter-sites.json"; fi
  if [ $COLD = 1 ]; then rm -f "$RIG/bin/goatcounter-$GOATCOUNTER_VERSION" "$GC_BIN"; fi
  return 0
}

for p in $(products "$target"); do "down_$p"; done
