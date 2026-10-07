#!/usr/bin/env bash
set -euo pipefail
PHASE=panel-small; source "$(dirname "$0")/../lib.sh"
say "ETA: 15-25 minutes"
ART="$RESULTS_DIR/artifacts/panel-small"; cluster=panel-final
plan "sudo pg_createcluster PostgreSQL 18 $cluster on :5433; shared_buffers=128MB max_connections=20"
plan "one systemd-run --scope with MemoryMax=1G CPUQuota=100% contains PG, proto app, site-1 Tier S 30d load, S2/S3 timing"
plan "capture memory.current/memory.peak/memory.events; stop and drop cluster"
(( DRY_RUN )) && dry_finish
require systemd-run; require pg_createcluster; sudo -n true
fresh_artifacts "$ART"
cleanup(){ sudo -n pg_ctlcluster 18 "$cluster" stop >/dev/null 2>&1 || true; sudo -n pg_dropcluster --stop 18 "$cluster" >/dev/null 2>&1 || true; }
trap cleanup EXIT
cleanup
sudo -n pg_createcluster 18 "$cluster" --port 5433 --start-conf=manual >/dev/null
sudo -n pg_conftool 18 "$cluster" set shared_buffers 128MB
sudo -n pg_conftool 18 "$cluster" set max_connections 20
quiet_gate
sudo -n systemd-run --scope --unit=panel-final-small.scope -p MemoryMax=1G -p CPUQuota=100% \
  "$FINAL_DIR/small-inner.sh" "$ART" "$cluster"
cleanup; trap - EXIT
python3 -I - "$ART/meta.json" "$ART" <<'PY'
import glob,json,os,sys
d=sys.argv[2]; c=json.load(open(d+'/cgroup.json')); ev={}
for line in c.get('memory.events','').splitlines():
 p=line.split();
 if len(p)==2: ev[p[0]]=int(p[1])
json.dump({'limit_split':{'memory_bytes':1073741824,'cpu_quota_percent':100},'cgroup':c,'oom_events':ev.get('oom',0)+ev.get('oom_kill',0),'queries':[os.path.basename(x) for x in glob.glob(d+'/final-panel-small-*.json')]},open(sys.argv[1],'w'),indent=2)
PY
finish complete "$ART/meta.json"
