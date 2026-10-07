#!/usr/bin/env bash
set -euo pipefail
PHASE=env; source "$(dirname "$0")/../lib.sh"
say "ETA: under 1 minute"
plan "record loadavg, CPU, memory, disk type/free space, PostgreSQL and tool versions/settings"
(( DRY_RUN )) && dry_finish
require python3; require psql
tmp=$(mktemp); trap 'rm -f "$tmp"' EXIT
python3 -I - "$tmp" "$ROOT" <<'PY'
import json, os, pathlib, platform, shutil, subprocess, sys
out, root = sys.argv[1:]
def cmd(*a):
    try: return subprocess.check_output(a, text=True, stderr=subprocess.STDOUT).strip()
    except Exception as e: return f"unavailable: {e}"
disk=[]
for p in pathlib.Path('/sys/block').glob('*/queue/rotational'):
    try: disk.append({"device":p.parts[-3],"rotational":p.read_text().strip()=="1"})
    except OSError: pass
q="show server_version; show shared_buffers; show work_mem; show effective_cache_size; show io_workers; show jit; show max_connections"
x={"loadavg":open('/proc/loadavg').read().strip(),"nproc":os.cpu_count(),"memory":cmd('free','-b'),"disk":cmd('df','-B1',root),"disk_devices":disk,
   "postgres_settings":cmd('psql','postgresql://panel:panel@127.0.0.1:5432/panel?sslmode=disable','-At','-c',q),
   "versions":{"kernel":platform.platform(),"psql":cmd('psql','--version'),"go":cmd('go','version'),"docker":cmd('docker','--version'),"compose":cmd('docker','compose','version'),"python":platform.python_version()},
   "binaries":{p:cmd(p,'--help')[:200] for p in [root+'/bench/proto/proto',root+'/bench/load/bin/gen',root+'/bench/load/bin/harness'] if os.path.exists(p)}}
json.dump(x,open(out,'w'),indent=2); open(out,'a').write('\n')
PY
finish complete "$tmp"

