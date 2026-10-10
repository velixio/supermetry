#!/usr/bin/env bash
# O2: scripted, timed "clean install -> first pageview visible" for one product.
#   --cold  (default) wipe data AND install artefacts first (images / CE clone / binary): includes download
#   --warm  wipe data only (images/binary cached): measures start + migrate + seed + first pageview
# Phases timed: install+start (up.sh) -> admin + site1 (seed.sh <p> 1) -> one pageview sent through the
# real tracking API and polled until visible in storage (pageview.sh). Docker and the local PG are
# prerequisites, as in the scorecard ("Postgres already present"). Afterwards the remaining sites are
# seeded, untimed. Writes tools/bench/results/o2-<product>-<mode>-<run>.json.
set -euo pipefail
. "$(dirname "$0")/lib.sh"
p=${1:?usage: o2-install.sh plausible|umami|goatcounter [--cold|--warm]}; mode=${2:---cold}; mode=${mode#--}
products "$p" >/dev/null
[ "$mode" = cold ] || [ "$mode" = warm ] || die "mode must be --cold or --warm"
RIG_PRODUCT=$p

log "O2 $mode: wiping $p"
"$RIG/down.sh" "$p" "--$([ "$mode" = cold ] && echo cold || echo purge)" >/dev/null 2>&1
sync; sleep 2

t0=$(now_ms)
"$RIG/up.sh" "$p";            t1=$(now_ms)
"$RIG/seed.sh" "$p" 1;        t2=$(now_ms)
pv=$("$RIG/pageview.sh" "$p" 1); t3=$(now_ms)
log "O2 $mode total $(( (t3 - t0) / 1000 )).$(( (t3 - t0) % 1000 / 100 ))s"

mkdir -p "$RESULTS"
i=1; while [ -e "$RESULTS/o2-$p-$mode-$i.json" ]; do i=$((i + 1)); done
out="$RESULTS/o2-$p-$mode-$i.json"
PV="$pv" python3 -I - "$out" "$p" "$mode" "$t0" "$t1" "$t2" "$t3" <<'PY'
import json, os, sys, datetime, platform
out, p, mode, t0, t1, t2, t3 = sys.argv[1], sys.argv[2], sys.argv[3], *map(int, sys.argv[4:])
json.dump({
    "metric": "O2", "product": p, "mode": mode,
    "date": datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds"),
    "host": platform.node(),
    "phases_ms": {"install_and_start": t1 - t0, "admin_and_site1": t2 - t1, "first_pageview_visible": t3 - t2},
    "total_ms": t3 - t0,
    "pageview": json.loads(os.environ["PV"]),
}, open(out, "w"), indent=1)
print(open(out).read())
PY
"$RIG/seed.sh" "$p" >/dev/null   # remaining sites, untimed
