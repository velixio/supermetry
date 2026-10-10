#!/usr/bin/env bash
# Start/stop GoatCounter serve like tools/bench/rig/up.sh does, plus an optional
# -ratelimit override (a documented deviation; see tools/bench/load/NOTES.md).
# Usage: serve.sh start [ratelimit-spec] | stop
# The pid lives in tools/bench/load/data/goatcounter.pid (rig's status.sh won't see it).
set -euo pipefail
LOAD="$(cd "$(dirname "$0")/.." && pwd)"
. "$LOAD/../rig/lib.sh"
PIDF="$LOAD/data/goatcounter.pid"
case ${1:-} in
  start)
    RL=${2:-}
    if [ -f "$PIDF" ] && kill -0 "$(cat "$PIDF")" 2>/dev/null; then die "already running"; fi
    gc_running && die "rig's goatcounter is running; run tools/bench/rig/down.sh goatcounter first"
    args=(serve -db "$(gc_db)" -listen "127.0.0.1:$GOATCOUNTER_PORT" -tls http -automigrate)
    [ -n "$RL" ] && args+=(-ratelimit "$RL")
    PGPASSWORD="$GOATCOUNTER_DB_PASSWORD" setsid nohup "$GC_BIN" "${args[@]}" \
      >>"$LOAD/data/goatcounter-serve.log" 2>&1 </dev/null &
    echo $! >"$PIDF"
    wait_http "http://127.0.0.1:$GOATCOUNTER_PORT/status" 60 || die "goatcounter not up; see data/goatcounter-serve.log"
    log "goatcounter serve pid $(cat "$PIDF") ratelimit='${RL:-default}'" ;;
  stop)
    if [ -f "$PIDF" ]; then
      pid=$(cat "$PIDF")
      kill "$pid" 2>/dev/null || true
      for _ in $(seq 100); do kill -0 "$pid" 2>/dev/null || break; sleep 0.2; done
      kill -0 "$pid" 2>/dev/null && kill -9 "$pid"
      rm -f "$PIDF"
      log "stopped $pid"
    fi ;;
  *) echo "usage: serve.sh start [ratelimit] | stop" >&2; exit 2 ;;
esac
