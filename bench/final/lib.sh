#!/usr/bin/env bash
set -euo pipefail

FINAL_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ROOT=$(cd "$FINAL_DIR/../.." && pwd)
RESULTS_DIR=${FINAL_RESULTS_DIR:-$ROOT/bench/results/final}
STATE_DIR="$RESULTS_DIR/.state"
DRY_RUN=${DRY_RUN:-0}
PHASE=${PHASE:?PHASE must be set}
LOG="$RESULTS_DIR/$PHASE.log"
JSON="$RESULTS_DIR/$PHASE.json"
COMMANDS="$STATE_DIR/$PHASE.commands"

mkdir -p "$RESULTS_DIR" "$STATE_DIR"
: >"$COMMANDS"
exec > >(tee -a "$LOG") 2>&1

stamp() { date -u +%Y-%m-%dT%H:%M:%SZ; }
say() { printf '[%s] %s\n' "$(stamp)" "$*"; }
plan() { printf '%s\n' "$*" >>"$COMMANDS"; say "PLAN $*"; }

finish() {
  local status=${1:-complete} extra=${2:-}
  python3 -I - "$JSON" "$PHASE" "$status" "$COMMANDS" "$extra" <<'PY'
import json, os, sys
out, phase, status, commands, extra = sys.argv[1:]
with open(commands, encoding="utf-8") as f:
    cmds = [x.rstrip("\n") for x in f if x.strip()]
x = {"phase": phase, "status": status, "measured_at": __import__('datetime').datetime.now(__import__('datetime').timezone.utc).isoformat(), "commands": cmds}
if extra and os.path.exists(extra):
    with open(extra, encoding="utf-8") as f: x["result"] = json.load(f)
with open(out + ".tmp", "w", encoding="utf-8") as f: json.dump(x, f, indent=2); f.write("\n")
os.replace(out + ".tmp", out)
PY
  if [[ $status == complete ]]; then : >"$STATE_DIR/$PHASE.done"; fi
}

dry_finish() { finish dry-run; exit 0; }

quiet_gate() {
  local deadline=$((SECONDS + 600)) load
  while :; do
    load=$(cut -d' ' -f1 /proc/loadavg)
    if awk -v x="$load" 'BEGIN { exit !(x <= 2.0) }'; then
      say "timing gate open: load1=$load"
      NOISY=false
      return 0
    fi
    if (( SECONDS >= deadline )); then
      say "timing gate timeout: load1=$load; recording noisy=true"
      NOISY=true
      : >"$STATE_DIR/$PHASE.noisy"
      return 0
    fi
    say "load1=$load > 2.0; waiting (up to 10 minutes)"
    sleep 15
  done
}

fresh_artifacts() {
  local path=$1 archive="$RESULTS_DIR/artifacts/.archive"
  if [[ -e $path ]]; then
    mkdir -p "$archive"
    mv "$path" "$archive/$(basename "$path")-$(date -u +%Y%m%dT%H%M%SZ)-$$"
  fi
  mkdir -p "$path"
}

require() { command -v "$1" >/dev/null || { say "missing required tool: $1"; exit 1; }; }
latest() { find "$1" -maxdepth 1 -type f -name "$2" -printf '%T@ %p\n' | sort -nr | head -1 | cut -d' ' -f2-; }
