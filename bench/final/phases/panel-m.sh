#!/usr/bin/env bash
set -euo pipefail
PHASE=panel-m; source "$(dirname "$0")/../lib.sh"
say "ETA: 35-55 minutes"
ART="$RESULTS_DIR/artifacts/panel-m"
PROTO="$ROOT/bench/proto/proto"; GEN="$ROOT/bench/load/bin/gen"
plan "generate Tier M truth only (seed 42; existing panel data is not modified)"
plan "quiet-gated proto bench: S2 14/350/365d, S3 14d, S4/F4 14d; warmup=3 runs=30 site=1"
plan "proto verify C1 against Tier M truth"
(( DRY_RUN )) && dry_finish
fresh_artifacts "$ART"
require psql; [[ -x $PROTO && -x $GEN ]] || { say "build proto and gen first"; exit 1; }
read -r events first last rollup_days site1_days months blocks block_rows <<<"$(psql 'postgresql://panel:panel@127.0.0.1:5432/panel?sslmode=disable' -At -F' ' -c "select (select count(*) from events),(select min(local_date) from events),(select max(local_date) from events),(select count(*) from rollup_day where day between date '2026-09-17' and date '2026-09-30'),(select count(*) from rollup_day where site_id=1 and day between date '2025-10-01' and date '2026-09-30'),(select count(*) from rollup_month_done where site_id=1 and month between date '2025-10-01' and date '2026-09-01'),(select count(*) from rollup_block_done where site_id=1),(select count(*) from rollup_block_dim where site_id=1)")"
[[ $events == 140339436 && $first == 2026-09-17 && $last == 2026-09-30 && $rollup_days == 1400 && $site1_days == 365 && $months == 12 && $blocks == 718 && $block_rows -gt 0 ]] || { say "Tier M preflight failed: events=$events dates=$first..$last raw_rollups=$rollup_days site1_days=$site1_days months=$months blocks=$blocks block_rows=$block_rows"; exit 1; }
"$GEN" --tier M --days 14 --seed 42 --out /dev/null --sites "$ART/sites.json" --truth "$ART/truth.json" --quiet
for spec in 's2|14,350,365|14|14' 's3||14|14' 's4,f4||14|14'; do
  IFS='|' read -r cases s2 s3 s4 <<<"$spec"
  quiet_gate
  args=(bench --tier M --site 1 --to 2026-09-30 --cases "$cases" --s3-days "$s3" --s4-days "$s4" --runs 30 --warmup 3 --kind final-panel-m --label "$cases-noisy=$NOISY" --out "$ART")
  [[ -n $s2 ]] && args+=(--s2-days "$s2")
  "$PROTO" "${args[@]}"
done
set +e; "$PROTO" verify --truth "$ART/truth.json" --workers 8 >"$ART/verify.json"; verify_rc=$?; set -e
python3 -I - "$ART/meta.json" "$NOISY" "$verify_rc" <<PY
import glob,json,sys,os
json.dump({'noisy':sys.argv[2]=='true','verify_exit':int(sys.argv[3]),'artifacts':[os.path.basename(x) for x in glob.glob('$ART/*.json')]},open(sys.argv[1],'w'),indent=2)
PY
finish complete "$ART/meta.json"
