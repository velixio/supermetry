#!/usr/bin/env bash
set -euo pipefail
PHASE=summary; source "$(dirname "$0")/../lib.sh"
say "ETA: under 1 minute"
plan "mechanically render every scorecard line; final Plausible, dev Umami/GoatCounter, prep O2-O5/R5/P2-P4; emit GO/NO-GO/BLOCKED"
(( DRY_RUN )) && dry_finish
python3 -I "$FINAL_DIR/summary.py" "$RESULTS_DIR" "$RESULTS_DIR/summary.md" "$RESULTS_DIR/summary-data.json"
finish complete "$RESULTS_DIR/summary-data.json"

