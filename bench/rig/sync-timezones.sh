#!/usr/bin/env bash
# Set each incumbent site's time zone from the generator's sites.json
# ([{id, host, timezone, share}], matched on host "siteN.test").
# Usage: sync-timezones.sh <sites.json> [plausible|umami|goatcounter|all]   (default: all)
#   plausible   : UPDATE sites.timezone (+updated_at, so the 30s site-cache refresh picks it up)
#   umami       : nothing to store. Umami has no per-website zone; the query harness must pass
#                 ?timezone=<tz> on every stats call (website table has no tz column).
#   goatcounter : the zone is a per-USER setting (users.settings->timezone = "CC.Area/City");
#                 seed.sh gives every site its own user, so this is per site. Restarts serve if up.
#                 Note GoatCounter buckets hit_counts by UTC hour, so +5:30/+5:45 can't be exact.
set -euo pipefail
. "$(dirname "$0")/lib.sh"
SJ=${1:?usage: sync-timezones.sh <sites.json> [product]}; target=${2:-all}
[ -f "$SJ" ] || die "no such file: $SJ"

# host<TAB>tz<TAB>goatcounter-zone, validated (IANA via zoneinfo; strict charset, so safe in SQL).
pairs=$(python3 -I - "$SJ" <<'PY'
import json, re, sys
from zoneinfo import ZoneInfo
cc = {}
try:
    for line in open("/usr/share/zoneinfo/zone.tab"):
        if line.startswith("#"): continue
        f = line.rstrip("\n").split("\t")
        if len(f) >= 3: cc.setdefault(f[2], f[0])
except OSError: pass
for s in json.load(open(sys.argv[1])):
    host, tz = s["host"], s.get("timezone") or "UTC"
    if not re.fullmatch(r"site[0-9]+\.test", host): sys.exit(f"bad host {host!r}")
    if not re.fullmatch(r"[A-Za-z0-9_/+-]+", tz): sys.exit(f"bad tz {tz!r}")
    ZoneInfo(tz)  # raises on unknown zones
    gz = ".UTC" if tz in ("UTC", "Etc/UTC") else f"{cc.get(tz, '')}.{tz}"
    print(f"{host}\t{tz}\t{gz}")
PY
)
values() { awk -F'\t' -v col="$1" '{printf "%s(\x27%s\x27,\x27%s\x27)", (NR>1?",":""), $1, $col}' <<<"$pairs"; }
log "$(wc -l <<<"$pairs") sites in $SJ"

sync_plausible() {
  RIG_PRODUCT=plausible
  local n; n=$(p_psql -c "WITH v(host,tz) AS (VALUES $(values 2)) UPDATE sites s SET timezone = v.tz, updated_at = now() FROM v WHERE s.domain = v.host AND s.timezone IS DISTINCT FROM v.tz RETURNING 1" | wc -l)
  log "updated $n sites (sites.timezone); the site cache refreshes within ~30s"
}

sync_umami() {
  RIG_PRODUCT=umami
  log "no-op: Umami has no per-website time zone; pass ?timezone=<tz> per query (see sites.json)"
}

sync_goatcounter() {
  RIG_PRODUCT=goatcounter
  local n; n=$(pg_as goatcounter goatcounter -c "WITH v(host,tz) AS (VALUES $(values 3)) UPDATE users u SET settings = jsonb_set(u.settings::jsonb, '{timezone}', to_jsonb(v.tz)) FROM sites s JOIN v ON s.cname = v.host WHERE u.site_id = s.site_id AND (u.settings::jsonb->>'timezone') IS DISTINCT FROM v.tz RETURNING 1" | wc -l)
  log "updated $n users (users.settings.timezone)"
  if [ "$n" -gt 0 ] && gc_running; then log "restarting serve to drop cached users"; "$RIG/down.sh" goatcounter && "$RIG/up.sh" goatcounter; fi
}

for p in $(products "$target"); do "sync_$p"; done
