#!/usr/bin/env bash
# Import gen Tier S into GoatCounter via /api/v0/count (bench/load/NOTES.md).
# Usage: [GC_WORKERS=1] [GC_BATCH=500] load.sh [days] [only-site]
# One request at a time: concurrent /api/v0/count requests make v2.7.0 panic
# ("sync: WaitGroup is reused before previous Wait has returned"); `goatcounter
# import` is sequential too.
set -euo pipefail
LOAD="$(cd "$(dirname "$0")/.." && pwd)"
. "$LOAD/../rig/lib.sh"
DAYS=${1:-30}; ONLY=${2:-0}
(cd "$LOAD" && GOPROXY=off go build -o bin/goatcounter-load ./goatcounter)
"$RIG/up.sh" goatcounter      # installs/migrates if needed
"$RIG/down.sh" goatcounter    # stop the rig's default-flag serve; data kept
"$RIG/sync-timezones.sh" "$LOAD/data/sites.json" goatcounter
gq() { pg_as goatcounter goatcounter "$@"; }
pg_admin -c "ALTER ROLE goatcounter SET work_mem = '32MB'" -c "ALTER ROLE goatcounter SET max_parallel_workers_per_gather = 4"

TOK="$LOAD/data/goatcounter-tokens.json"
if [ ! -s "$TOK" ]; then
  python3 -I -c 'import json,sys; [print(r["user_id"]) for r in json.load(open(sys.argv[1]))]' "$RIG/goatcounter-sites.json" |
    while read -r uid; do
      gc db create apitoken -db "$(gc_db)" -user "$uid" -name bench-load -perm count,site_read >/dev/null
    done
  # The CLI has no "stats" permission (the web UI does); 74 = count|site_read|stats, found empirically.
  gq -c "UPDATE api_tokens SET permissions = '74' WHERE name = 'bench-load'"
  (umask 077; gq -c "SELECT json_object_agg(s.cname, t.token) FROM api_tokens t JOIN users u ON u.user_id = t.user_id JOIN sites s ON s.site_id = u.site_id WHERE t.name = 'bench-load'" |
    python3 -I -c 'import json,sys; d=json.load(sys.stdin); json.dump({h[4:-5]: t for h, t in d.items()}, open(sys.argv[1], "w"))' "$TOK")
fi
log "tokens for $(python3 -I -c 'import json,sys; print(len(json.load(open(sys.argv[1]))))' "$TOK") sites"

"$LOAD/goatcounter/serve.sh" start "count:1000000/1,api:1000000/1,api-count:1000000/1"
t0=$(date +%s)
"$LOAD/bin/gen" --tier S --days "$DAYS" --seed 42 --out - --sites "$LOAD/data/sites.json" --truth /dev/null --quiet |
  "$LOAD/bin/goatcounter-load" --tokens "$TOK" --sites "$LOAD/data/sites.json" --only-site "$ONLY" \
    --workers "${GC_WORKERS:-1}" --batch "${GC_BATCH:-500}"
t1=$(date +%s)
log "import requests done in $((t1 - t0)) s; waiting for the memstore to persist"
prev=-1
for _ in $(seq 1 120); do
  cur=$(gq -c "SELECT coalesce(sum(total),0) FROM hit_counts")
  [ "$cur" = "$prev" ] && break
  prev=$cur; sleep 15
done
t2=$(date +%s)
log "hit_counts stable at $cur after $((t2 - t1)) s"
echo "{\"import_s\":$((t1 - t0)),\"persist_wait_s\":$((t2 - t1))}"
gq -c "VACUUM ANALYZE"
gq -c "SELECT pg_database_size('goatcounter')"
