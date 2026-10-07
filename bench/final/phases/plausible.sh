#!/usr/bin/env bash
set -euo pipefail
PHASE=plausible; source "$(dirname "$0")/../lib.sh"
say "ETA: 35-55 minutes"
ART="$RESULTS_DIR/artifacts/plausible"
RIG="$ROOT/bench/rig"; LOAD="$ROOT/bench/load"
plan "purge/start/seed Plausible CE; Tier S 90d direct ClickHouse load; recreate API key and backdate native stats"
plan "quiet-gated harness S2/S3 at 30d and 90d (3 warmup + 30 runs)"
plan "R2 HTTP 1K/s 10m with docker stats; then 1GiB/1CPU split CH 640MiB/.60, app 256MiB/.25, PG 128MiB/.15; S2 30d and OOM state; purge"
(( DRY_RUN )) && dry_finish
fresh_artifacts "$ART"
source "$RIG/lib.sh"; sudo -n true
cleanup(){ "$RIG/down.sh" plausible --purge >/dev/null 2>&1 || true; }; trap cleanup EXIT
"$RIG/down.sh" plausible --purge || true
"$RIG/up.sh" plausible; "$RIG/seed.sh" plausible 100
t0=$(date +%s); "$LOAD/plausible/load.sh" 90 | tee "$ART/load.out"; load_s=$(( $(date +%s)-t0 ))
key=$(openssl rand -hex 32)
p_rpc "user=Plausible.Repo.get_by!(Plausible.Auth.User,email: \"$PLAUSIBLE_ADMIN_EMAIL\"); team=Plausible.Repo.one!(Ecto.assoc(user,:teams)); Plausible.Auth.ApiKey.changeset(%Plausible.Auth.ApiKey{user_id: user.id},team,%{name: \"final-bench\",key: \"$key\"}) |> Plausible.Repo.insert!()" >/dev/null
printf '%s\n' "$key" >"$ART/api-key"; chmod 600 "$ART/api-key"
p_psql -c "UPDATE sites SET native_stats_start_at='2026-07-01', stats_start_date='2026-07-01'; UPDATE api_keys SET hourly_request_limit=1000000;"
for range in '30|2026-09-01|2026-09-30' '90|2026-07-03|2026-09-30'; do
  IFS='|' read -r days from to <<<"$range"; quiet_gate
  (cd "$LOAD" && PLAUSIBLE_API_KEY_FILE="$ART/api-key" bin/harness bench -product plausible -site 1 -from "$from" -to "$to" -runs 30 -warmup 3 -budget 0 -only S2,S3 -sites data/sites.json -label "final-tierS${days}-noisy=$NOISY" -out "$ART/queries-${days}d.json")
done
quiet_gate
"$LOAD/memsample.sh" plausible 630 10 "$ART/r2-memory.jsonl" & mp=$!; sleep 20
(cd "$LOAD" && bin/loadtest -product plausible -sites "$RIG/plausible-sites.json" -rate 1000 -duration 10m -concurrency 256 -timeout 5s -label final-r2 -out "$ART/r2.json" < <(bin/gen --tier S --days 1 --start 2026-10-01 --seed 42 --out - --sites /dev/null --truth /dev/null --quiet))
wait "$mp"
mapfile -t cts < <("${D[@]}" ps --format '{{.Names}}' | grep '^plausible-')
ch=$(printf '%s\n' "${cts[@]}" | grep plausible_events_db | head -1); app=$(printf '%s\n' "${cts[@]}" | grep -E '^plausible-plausible-[0-9]+$' | head -1); pg=$(printf '%s\n' "${cts[@]}" | grep plausible_db | head -1)
"${D[@]}" update --memory 640m --memory-swap 640m --cpus .60 "$ch" >/dev/null
"${D[@]}" update --memory 256m --memory-swap 256m --cpus .25 "$app" >/dev/null
"${D[@]}" update --memory 128m --memory-swap 128m --cpus .15 "$pg" >/dev/null
"${D[@]}" inspect "$ch" "$app" "$pg" >"$ART/r4-inspect-before.json"
quiet_gate
set +e
(cd "$LOAD" && PLAUSIBLE_API_KEY_FILE="$ART/api-key" bin/harness bench -product plausible -site 1 -from 2026-09-01 -to 2026-09-30 -runs 30 -warmup 3 -budget 0 -only S2 -sites data/sites.json -label "final-r4-noisy=$NOISY" -out "$ART/r4-queries.json")
r4_rc=$?
set -e
"${D[@]}" stats --no-stream --format '{{json .}}' "$ch" "$app" "$pg" >"$ART/r4-stats.jsonl" || : >"$ART/r4-stats.jsonl"
"${D[@]}" inspect "$ch" "$app" "$pg" >"$ART/r4-inspect-after.json"
python3 -I - "$ART/meta.json" "$load_s" "$ART" "$r4_rc" <<'PY'
import json,statistics,sys
out,load,d,r4rc=sys.argv[1:]
rows=[json.loads(x) for x in open(d+'/r2-memory.jsonl')]
sums=[sum(r.get('docker_mib',{}).values()) for r in rows]
after=json.load(open(d+'/r4-inspect-after.json'))
json.dump({'load_seconds':int(load),'r2_stack_docker_mib':{'p50':statistics.median(sums),'max':max(sums)},'r4_split':{'clickhouse_mib':640,'app_mib':256,'postgres_mib':128,'cpu':[.60,.25,.15]},'r4_harness_exit':int(r4rc),'r4_oom_killed':[x['State'].get('OOMKilled') for x in after]},open(out,'w'),indent=2)
PY
cleanup; trap - EXIT
finish complete "$ART/meta.json"
