#!/usr/bin/env python3
"""Sum memory of process groups. stdin: {"group": [pid, ...], ...}; stdout: JSON per group.

Run as root (sudo python3 -I memstat.py) so /proc/<pid>/smaps_rollup of other users'
processes (postgres, container processes) is readable. Values in KiB:
  rss       VmRSS (counts shared pages, e.g. PG shared_buffers touched, in every process)
  rss_anon  RssAnon: private-ish anonymous memory, the best "what this process costs" number
  rss_shmem RssShmem: shared memory mapped (PG shared_buffers)
  pss       proportional set size (shared pages divided among their users; sums correctly)
"""
import json
import sys


def status(pid):
    out = {}
    with open(f"/proc/{pid}/status") as f:
        for line in f:
            k, _, v = line.partition(":")
            if k in ("VmRSS", "RssAnon", "RssFile", "RssShmem"):
                out[k] = int(v.split()[0])
    with open(f"/proc/{pid}/smaps_rollup") as f:
        for line in f:
            if line.startswith("Pss:"):
                out["Pss"] = int(line.split()[1])
    return out


groups = json.load(sys.stdin)
res = {}
for name, pids in groups.items():
    tot = {"n": 0, "rss": 0, "rss_anon": 0, "rss_shmem": 0, "pss": 0}
    for pid in pids:
        try:
            s = status(int(pid))
        except (FileNotFoundError, ProcessLookupError, PermissionError):
            continue
        tot["n"] += 1
        tot["rss"] += s.get("VmRSS", 0)
        tot["rss_anon"] += s.get("RssAnon", 0)
        tot["rss_shmem"] += s.get("RssShmem", 0)
        tot["pss"] += s.get("Pss", 0)
    res[name] = tot
json.dump(res, sys.stdout)
