#!/usr/bin/env bash
set -euo pipefail
PHASE=supermetry-s; source "$(dirname "$0")/../lib.sh"
say "ETA: 25-40 minutes"
ART="$RESULTS_DIR/artifacts/supermetry-s"
PROTO="$ROOT/bench/proto/proto"; GEN="$ROOT/bench/load/bin/gen"
plan "DESTRUCTIVE: proto reset on the supermetry public schema, then stream Tier S 90d seed 42 into proto load"
plan "close 2026-07-03..2026-09-30 with --drop-hot; build month and dyadic rollups"
plan "quiet-gated S2/S3/S4/F4 at 30d and 90d, 3 warmup + 30 runs; C1 verify; R3 bytes/event"
(( DRY_RUN )) && dry_finish
fresh_artifacts "$ART"
[[ -x $PROTO && -x $GEN ]] || { say "build proto and gen first"; exit 1; }
"$PROTO" reset
t0=$(date +%s)
# sites.json must exist before proto load opens it (race fixed 2026-10-07): pre-generate it with the same seed and tier.
"$GEN" --tier S --days 1 --seed 42 --out /dev/null --sites "$ART/sites.json" --truth /dev/null --quiet
"$GEN" --tier S --days 90 --seed 42 --out - --sites "$ART/sites.json" --truth "$ART/truth.json" --quiet |
  "$PROTO" load --sites "$ART/sites.json"
load_s=$(( $(date +%s) - t0 ))
"$PROTO" rollup --from 2026-07-03 --to 2026-09-30 --parallel 4 --drop-hot
"$PROTO" rollup-months --from 2026-07-01 --to 2026-09-30
"$PROTO" rollup-blocks --from 2026-07-03 --to 2026-09-30
for spec in 's2|30,90|90|30' 's3||30|30' 's3||90|90' 's4,f4||30|30' 's4,f4||90|90'; do
  IFS='|' read -r cases s2 s3 s4 <<<"$spec"; quiet_gate
  args=(bench --tier S --site 1 --to 2026-09-30 --cases "$cases" --s3-days "$s3" --s4-days "$s4" --runs 30 --warmup 3 --kind final-supermetry-s --label "$cases-${s3}d-noisy=$NOISY" --out "$ART")
  [[ -n $s2 ]] && args+=(--s2-days "$s2")
  "$PROTO" "${args[@]}"
done
set +e; "$PROTO" verify --truth "$ART/truth.json" --workers 8 >"$ART/verify.json"; verify_rc=$?; set -e
bytes=$(psql 'postgresql://supermetry:supermetry@127.0.0.1:5432/supermetry?sslmode=disable' -At -c 'select pg_database_size(current_database())')
generated=$(python3 -I -c 'import json,sys;print(json.load(open(sys.argv[1]))["events"])' "$ART/truth.json")
python3 -I - "$ART/meta.json" "$load_s" "$bytes" "$generated" "$verify_rc" <<PY
import glob,json,sys,os
out,load,b,e,vrc=sys.argv[1:]; b=int(b); e=int(e)
json.dump({'load_seconds':int(load),'database_bytes':b,'generated_events':e,'bytes_per_million_events':b/e*1_000_000,'verify_exit':int(vrc),'artifacts':[os.path.basename(x) for x in glob.glob('$ART/*.json')]},open(out,'w'),indent=2)
PY
finish complete "$ART/meta.json"
