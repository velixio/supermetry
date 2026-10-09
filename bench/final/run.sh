#!/usr/bin/env bash
set -euo pipefail

FINAL=$(cd "$(dirname "$0")" && pwd)
usage() { echo "Usage: bench/final/run.sh <env|supermetry-m|supermetry-s|supermetry-ingest|supermetry-c2p1|supermetry-small|plausible|summary> [--dry-run]"; }
[[ $# -ge 1 && $# -le 2 ]] || { usage >&2; exit 2; }
phase=$1; shift
case $phase in env|supermetry-m|supermetry-s|supermetry-ingest|supermetry-c2p1|supermetry-small|plausible|summary) ;; *) usage >&2; exit 2;; esac
dry=0
if [[ $# == 1 ]]; then [[ $1 == --dry-run ]] || { usage >&2; exit 2; }; dry=1; fi
if [[ $dry == 1 && -z ${FINAL_RESULTS_DIR:-} ]]; then
  results="$FINAL/../results/final/dry-run"
else
  results=${FINAL_RESULTS_DIR:-$FINAL/../results/final}
fi
if [[ $dry == 0 && -f "$results/.state/$phase.done" && ${FINAL_FORCE:-0} != 1 ]]; then
  echo "$phase already complete; set FINAL_FORCE=1 to rerun"
  exit 0
fi
if [[ $dry == 0 ]]; then
  [[ ! -e "$results/.state/$phase.done" ]] || unlink "$results/.state/$phase.done"
  [[ ! -e "$results/.state/$phase.noisy" ]] || unlink "$results/.state/$phase.noisy"
fi
export PHASE=$phase DRY_RUN=$dry FINAL_RESULTS_DIR=$results
exec "$FINAL/phases/$phase.sh"
