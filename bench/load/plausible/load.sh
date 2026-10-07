#!/usr/bin/env bash
# Bulk-load gen Tier S into Plausible CE's ClickHouse events_v2 / sessions_v2
# (bench/load/NOTES.md). Usage: load.sh [days]
set -euo pipefail
LOAD="$(cd "$(dirname "$0")/.." && pwd)"
. "$LOAD/../rig/lib.sh"
DAYS=${1:-30}
(cd "$LOAD" && GOPROXY=off go build -o bin/plausible-load ./plausible)
"$RIG/up.sh" plausible
"$RIG/sync-timezones.sh" "$LOAD/data/sites.json" plausible
CT=$("${D[@]}" ps --format '{{.Names}}' | grep -E '^plausible-plausible_events_db-[0-9]+$' | head -1)
log "ClickHouse container: $CT"
p_ch "TRUNCATE TABLE events_v2"
p_ch "TRUNCATE TABLE sessions_v2"
t0=$(date +%s)
"$LOAD/bin/gen" --tier S --days "$DAYS" --seed 42 --out - --sites "$LOAD/data/sites.json" --truth /dev/null --quiet |
  "$LOAD/bin/plausible-load" --sites "$LOAD/data/sites.json" --container "$CT"
t1=$(date +%s)
log "insert done in $((t1 - t0)) s; waiting for background merges to settle"
for _ in $(seq 1 360); do
  m=$(p_ch "SELECT count() FROM system.merges WHERE database = 'plausible_events_db'")
  [ "$m" = 0 ] && break
  sleep 5
done
t2=$(date +%s)
log "merges idle after $((t2 - t1)) s"
# Goal for the F4 props breakdown (Stats API needs event:goal = signup).
p_rpc 'site = Plausible.Repo.get_by!(Plausible.Site, domain: "site1.test"); case Plausible.Goals.create(site, %{"event_name" => "signup"}) do {:ok, _} -> IO.puts("goal created"); other -> IO.inspect(other) end' 2>&1 | tail -1
echo "{\"insert_s\":$((t1 - t0)),\"merge_wait_s\":$((t2 - t1))}"
p_ch "SELECT table, sum(rows), sum(bytes_on_disk), count() FROM system.parts WHERE database = 'plausible_events_db' AND active AND table IN ('events_v2','sessions_v2') GROUP BY table FORMAT TSV"
