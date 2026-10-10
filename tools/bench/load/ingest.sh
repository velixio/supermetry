#!/usr/bin/env bash
# R2 (RSS at ~1K events/s for 5 min) and S1 (max sustained events/s, p99) through
# a product's real HTTP tracking API. Events come from gen tier S day 2026-10-01
# (after the loaded range); the APIs stamp server time.
# Usage: ingest.sh umami|plausible|goatcounter r2|s1 [rates...] [-- extra loadtest flags]
set -euo pipefail
LOAD="$(cd "$(dirname "$0")" && pwd)"
. "$LOAD/../rig/lib.sh"
P=$1 MODE=$2; shift 2
SITES="$RIG/$P-sites.json"
TS=$(date -u +%Y%m%d-%H%M%S)
OUTD="$LOAD/../results"
feed() { "$LOAD/bin/gen" --tier S --days 1 --start 2026-10-01 --seed 42 --out - --sites /dev/null --truth /dev/null --quiet; }
EXTRA=()
RATES=()
while [ $# -gt 0 ]; do
  if [ "$1" = "--" ]; then shift; EXTRA=("$@"); break; fi
  RATES+=("$1"); shift
done
case $MODE in
  r2)
    MS="$LOAD/data/r2-$P-$TS-mem.jsonl"
    "$LOAD/memsample.sh" "$P" 330 10 "$MS" &
    msp=$!
    sleep 20 # idle baseline samples first
    feed | "$LOAD/bin/loadtest" -product "$P" -sites "$SITES" -rate 1000 -duration 5m "${EXTRA[@]}" \
      -label "R2 1K ev/s 5 min" -out "$OUTD/incumbents-r2-$P-$TS.json" || true # gen gets SIGPIPE
    wait $msp || true
    cp "$MS" "$OUTD/incumbents-r2-$P-$TS-mem.jsonl"
    log "R2 written: incumbents-r2-$P-$TS.json (+ -mem.jsonl)" ;;
  s1)
    for r in "${RATES[@]}"; do
      feed | "$LOAD/bin/loadtest" -product "$P" -sites "$SITES" -rate "$r" -duration 30s "${EXTRA[@]}" \
        -label "S1 step $r ev/s 30 s" -out "$OUTD/incumbents-s1-$P-$TS-$r.json" || true
      sleep 15 # let the product drain its buffers between steps
    done ;;
esac
