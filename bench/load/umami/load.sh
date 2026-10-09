#!/usr/bin/env bash
# Bulk-load gen Tier S into Umami's PG tables (bench/load/NOTES.md).
# Usage: load.sh [days] [ndjson-file-instead-of-gen]
# Drops Umami's own indexes/PKs on the 3 tables, COPYs, then re-creates exactly
# the same DDL (saved from pg_indexes/pg_constraint), then VACUUM ANALYZE.
set -euo pipefail
LOAD="$(cd "$(dirname "$0")/.." && pwd)"
. "$LOAD/../rig/lib.sh"
DAYS=${1:-30}; SRC=${2:-}
export UMAMI_DSN="postgres://umami:${UMAMI_DB_PASSWORD}@127.0.0.1:${PGPORT_LOCAL}/umami?sslmode=disable"
DDL="$LOAD/data/umami-index-ddl.sql"
TABLES="'website_event','session','event_data'"
u() { pg_as umami umami "$@"; }

(cd "$LOAD" && go build -o bin/umami-load ./umami)

# Same per-role settings Supermetry's prototype uses (bench/proto/NOTES.md: session-tuned).
pg_admin -c "ALTER ROLE umami SET work_mem = '32MB'" -c "ALTER ROLE umami SET max_parallel_workers_per_gather = 4"

if [ ! -s "$DDL" ]; then
  u -c "SELECT CASE WHEN c.oid IS NOT NULL THEN format('ALTER TABLE %I ADD CONSTRAINT %I %s;', i.tablename, c.conname, pg_get_constraintdef(c.oid))
                    ELSE i.indexdef || ';' END
          FROM pg_indexes i
          LEFT JOIN pg_constraint c ON c.conname = i.indexname AND c.contype = 'p'
         WHERE i.schemaname = 'public' AND i.tablename IN ($TABLES) ORDER BY c.oid IS NULL, i.indexname" >"$DDL"
fi
log "saved $(wc -l <"$DDL") index/PK definitions to $DDL"

u -c "TRUNCATE website_event, session, event_data"
u -c "SELECT CASE WHEN c.oid IS NOT NULL THEN format('ALTER TABLE %I DROP CONSTRAINT %I', i.tablename, c.conname)
                  ELSE format('DROP INDEX %I', i.indexname) END
        FROM pg_indexes i LEFT JOIN pg_constraint c ON c.conname = i.indexname AND c.contype = 'p'
       WHERE i.schemaname = 'public' AND i.tablename IN ($TABLES)" | while read -r s; do u -c "$s"; done

t0=$(date +%s)
if [ -n "$SRC" ]; then
  "$LOAD/bin/umami-load" --sites "$RIG/umami-sites.json" <"$SRC"
else
  "$LOAD/bin/gen" --tier S --days "$DAYS" --seed 42 --out - --sites "$LOAD/data/sites.json" --truth /dev/null --quiet |
    "$LOAD/bin/umami-load" --sites "$RIG/umami-sites.json"
fi
t1=$(date +%s)
log "COPY done in $((t1 - t0)) s; re-creating $(wc -l <"$DDL") indexes (4 parallel sessions)"
# PKs first (they are listed first), then the rest, 4 at a time.
grep '^ALTER' "$DDL" | while read -r s; do u -c "SET maintenance_work_mem = '1GB'" -c "$s"; done
grep -v '^ALTER' "$DDL" | xargs -P 4 -I{} bash -c '. "$0/../rig/lib.sh"; pg_as umami umami -c "SET maintenance_work_mem = '"'"'1GB'"'"'" -c "$1"' "$LOAD" {}
t2=$(date +%s)
log "indexes done in $((t2 - t1)) s; VACUUM ANALYZE"
u -c "VACUUM (ANALYZE) website_event" -c "VACUUM (ANALYZE) session" -c "VACUUM (ANALYZE) event_data"
t3=$(date +%s)
log "vacuum analyze done in $((t3 - t2)) s"
echo "{\"copy_s\":$((t1 - t0)),\"index_s\":$((t2 - t1)),\"vacuum_s\":$((t3 - t2))}"
u -c "SELECT count(*) FROM pg_indexes WHERE schemaname='public' AND tablename IN ($TABLES)"
