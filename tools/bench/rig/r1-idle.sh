#!/usr/bin/env bash
# R1: idle memory of every running incumbent stack, after an idle period.
# Usage: r1-idle.sh [idle_seconds=300]. Writes tools/bench/results/r1-idle.json and prints a markdown table.
#  - containers: `docker stats --no-stream` MemUsage (cgroup usage minus inactive file cache) plus
#    the process RSS/PSS of the container's processes (docker top pids, read via /proc)
#  - GoatCounter: process RSS/PSS
#  - local PG (shared server): client backends per database (umami, goatcounter), and separately the
#    server's own processes (postmaster + aux), which no single product owns
set -euo pipefail
. "$(dirname "$0")/lib.sh"
IDLE=${1:-300}
log "idle ${IDLE}s before measuring ..."
sleep "$IDLE"

groups=$(mktemp); stats=$(mktemp); trap 'rm -f "$groups" "$stats"' EXIT
"${D[@]}" stats --no-stream --format '{{.Name}}\t{{.MemUsage}}\t{{.CPUPerc}}' >"$stats"

{
  echo '{'
  sep=''
  for c in $(cut -f1 "$stats"); do
    pids=$("${D[@]}" top "$c" -o pid | tail -n +2 | paste -sd, -)
    printf '%s"ct:%s":[%s]' "$sep" "$c" "$pids"; sep=','
  done
  gc_running && printf '%s"goatcounter":[%s]' "$sep" "$(cat "$GC_PID")" && sep=','
  for db in umami goatcounter; do
    pids=$(pg_admin -c "SELECT pid FROM pg_stat_activity WHERE datname = '$db' AND backend_type = 'client backend'" | paste -sd, -)
    printf '%s"pg:%s":[%s]' "$sep" "$db" "$pids"; sep=','
  done
  pm=$(sudo head -1 /var/lib/postgresql/18/main/postmaster.pid)
  clients=$(pg_admin -c "SELECT pid FROM pg_stat_activity WHERE backend_type = 'client backend'")
  aux=$( { echo "$pm"; ps -o pid= --ppid "$pm" | tr -d ' ' | grep -vxF -f <(echo "$clients"); } | paste -sd, -)
  printf '%s"pg:server":[%s]' "$sep" "$aux"
  others=$(pg_admin -c "SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'client backend' AND datname NOT IN ('umami','goatcounter','postgres')")
  echo '}'
} >"$groups"

mem=$(sudo python3 -I "$RIG/memstat.py" <"$groups")
mkdir -p "$RESULTS"
MEM="$mem" OTHERS="$others" IDLE="$IDLE" python3 -I - "$stats" "$RESULTS/r1-idle.json" <<'PY'
import json, os, sys, datetime, re
units = {"B": 1 / 2**20, "KiB": 1 / 1024, "kB": 1 / 1000, "MiB": 1, "MB": 1, "GiB": 1024, "GB": 1000}
def mib(s):
    m = re.match(r"([\d.]+)\s*([A-Za-z]+)", s.strip()); return round(float(m.group(1)) * units[m.group(2)], 1)
mem = json.loads(os.environ["MEM"])
ct = {}
for line in open(sys.argv[1]):
    name, usage, cpu = line.rstrip("\n").split("\t")
    g = mem.get(f"ct:{name}", {})
    ct[name] = {"docker_stats_mib": mib(usage.split("/")[0]), "cpu": cpu,
                "proc_rss_mib": round(g.get("rss", 0) / 1024, 1), "proc_pss_mib": round(g.get("pss", 0) / 1024, 1),
                "proc_anon_mib": round(g.get("rss_anon", 0) / 1024, 1), "nproc": g.get("n", 0)}
k = lambda g, f: round(mem.get(g, {}).get(f, 0) / 1024, 1)
pg = {g: {"n": mem.get(g, {}).get("n", 0), "rss_mib": k(g, "rss"), "pss_mib": k(g, "pss"), "anon_mib": k(g, "rss_anon")}
      for g in ("pg:umami", "pg:goatcounter", "pg:server")}
P = {n: v for n, v in ct.items() if n.startswith("plausible-")}
U = ct.get("bench-umami")
out = {"metric": "R1", "date": datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds"),
       "idle_s": int(os.environ["IDLE"]), "containers": ct, "goatcounter_proc": {
           "rss_mib": k("goatcounter", "rss"), "pss_mib": k("goatcounter", "pss"), "anon_mib": k("goatcounter", "rss_anon")},
       "local_pg": pg, "local_pg_other_app_client_backends": int(os.environ["OTHERS"] or 0)}
out["stack_mib"] = {
    "plausible": {"docker_stats": round(sum(v["docker_stats_mib"] for v in P.values()), 1),
                  "proc_rss": round(sum(v["proc_rss_mib"] for v in P.values()), 1)} if P else None,
    "umami": {"app_docker_stats": U["docker_stats_mib"], "app_proc_rss": U["proc_rss_mib"],
              "pg_backends_rss": pg["pg:umami"]["rss_mib"], "pg_backends_pss": pg["pg:umami"]["pss_mib"]} if U else None,
    "goatcounter": {"app_proc_rss": out["goatcounter_proc"]["rss_mib"],
                    "pg_backends_rss": pg["pg:goatcounter"]["rss_mib"], "pg_backends_pss": pg["pg:goatcounter"]["pss_mib"]}
                   if out["goatcounter_proc"]["rss_mib"] else None,
    "local_pg_server_shared": {"rss": pg["pg:server"]["rss_mib"], "pss": pg["pg:server"]["pss_mib"]},
}
json.dump(out, open(sys.argv[2], "w"), indent=1)
print("| component | docker stats MiB | process RSS MiB | PSS MiB | anon MiB | n |")
print("|---|---|---|---|---|---|")
for n, v in sorted(ct.items()):
    print(f"| {n} | {v['docker_stats_mib']} | {v['proc_rss_mib']} | {v['proc_pss_mib']} | {v['proc_anon_mib']} | {v['nproc']} |")
g = out["goatcounter_proc"]
if g["rss_mib"]: print(f"| goatcounter (host process) | n/a | {g['rss_mib']} | {g['pss_mib']} | {g['anon_mib']} | 1 |")
for n, v in pg.items():
    print(f"| local {n} | n/a | {v['rss_mib']} | {v['pss_mib']} | {v['anon_mib']} | {v['n']} |")
print(json.dumps(out["stack_mib"]))
PY
