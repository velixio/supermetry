#!/usr/bin/env python3
"""Print markdown tables from harness/loadtest result JSON files.

Usage: python3 -I summarize.py queries FILE...   (incumbents-queries-*.json)
       python3 -I summarize.py c1 FILE...        (incumbents-c1-*.json)
       python3 -I summarize.py s1 FILE...        (incumbents-s1-*.json)
       python3 -I summarize.py r2 FILE           (incumbents-r2-*-mem.jsonl)
"""
import json
import statistics
import sys


def fmt(ms):
    if ms is None:
        return "n/a"
    return f"{ms/1000:.2f} s" if ms >= 1000 else f"{ms:.0f} ms"


def queries(files):
    print("| product | case | runs | p50 | p95 | max | slowest panel (p95) | notes |")
    print("|---|---|---|---|---|---|---|---|")
    for f in files:
        d = json.load(open(f))
        for c in d["cases"]:
            if c.get("na"):
                print(f"| {d['product']} | {c['name']} | | n/a | n/a | n/a | | {c['na']}; {'; '.join(sorted(set(c.get('notes', {}).values())))} |")
                continue
            if c.get("error"):
                print(f"| {d['product']} | {c['name']} | | error | | | | {c['error'][:120]} |")
                continue
            w = c["wall"]
            slow = ""
            if c.get("panels"):
                n, v = max(c["panels"].items(), key=lambda kv: kv[1]["p95_ms"])
                slow = f"{n} {fmt(v['p95_ms'])}"
            notes = sorted(set(c.get("notes", {}).values()))
            if c.get("missing_panels"):
                notes.append("missing: " + ",".join(c["missing_panels"]))
            if c.get("runs_note"):
                notes.append(c["runs_note"])
            print(f"| {d['product']} | {c['name']} | {w['n']} | {fmt(w['p50_ms'])} | {fmt(w['p95_ms'])} | {fmt(w['max_ms'])} | {slow} | {'; '.join(notes)} |")


def panels(files):
    for f in files:
        d = json.load(open(f))
        names = ["overview", "timeseries", "pages", "sources", "countries", "browsers", "os", "devices", "entry", "exit"]
        print(f"\n{d['product']} per-panel p95:\n")
        print("| case | " + " | ".join(names) + " |")
        print("|---|" + "---|" * len(names))
        for c in d["cases"]:
            if not c.get("panels"):
                continue
            print(f"| {c['name']} | " + " | ".join(fmt(c["panels"][n]["p95_ms"]) if n in c["panels"] else "n/a" for n in names) + " |")


def c1(files):
    print("| product | site | date | tz | metric | truth | got | error |")
    print("|---|---|---|---|---|---|---|---|")
    for f in files:
        d = json.load(open(f))
        for r in d["c1"]:
            for k in ["visitors", "visits", "pageviews", "bounces", "duration_sum_s"]:
                g = r.get("got", {}).get(k, "n/a")
                e = r.get("error_vs_truth", {}).get(k)
                es = "n/a" if e is None else f"{e['abs']:+.0f} ({e.get('pct', 0):+.2f}%)"
                gs = g if isinstance(g, str) else f"{g:.0f}"
                print(f"| {d['product']} | {r['site']} | {r['date']} | {r['tz']} | {k} | {r['truth'][k if k != 'duration_sum_s' else 'duration_sum_s']:.0f} | {gs} | {es} |")


def s1(files):
    print("| product | target ev/s | achieved | ok/s | non-2xx | client overflow | p50 | p99 | max | sample error |")
    print("|---|---|---|---|---|---|---|---|---|---|")
    for f in sorted(files, key=lambda f: (json.load(open(f))["product"], json.load(open(f))["target_rate"])):
        d = json.load(open(f))
        l = d.get("latency_from_schedule") or {}
        bad = sum(v for k, v in d["codes"].items() if not k.startswith("2"))
        print(f"| {d['product']} | {d['target_rate']} | {d['achieved_rate']:.0f} | {d['ok_rate']:.0f} | {bad} | {d['client_overflow']} | "
              f"{l.get('p50_ms', 0):.1f} ms | {l.get('p99_ms', 0):.1f} ms | {l.get('max_ms', 0):.0f} ms | {str(d.get('error_sample', ''))[:80]} |")


def r2(path):
    rows = [json.loads(l) for l in open(path)]
    def mib(x):
        return x / 1024
    keys = ["app", "pg", "pg_server"]
    print("| sample | t | " + " | ".join(f"{k} rss/pss/anon MiB" for k in keys) + " | docker MiB |")
    print("|---|---|" + "---|" * (len(keys) + 1))
    for i, r in enumerate(rows):
        m = r["mem"]
        cells = [f"{mib(m[k]['rss']):.0f}/{mib(m[k]['pss']):.0f}/{mib(m[k]['rss_anon']):.0f} (n={m[k]['n']})" for k in keys]
        print(f"| {i} | {r['t']} | " + " | ".join(cells) + f" | {json.dumps(r['docker_mib'])} |")


if __name__ == "__main__":
    cmd, files = sys.argv[1], sys.argv[2:]
    {"queries": queries, "panels": panels, "c1": c1, "s1": s1}.get(cmd, lambda f: r2(f[0]))(files)
