#!/usr/bin/env bash
set -euo pipefail
[[ $# == 4 ]] || { echo 'Usage: memory-sample.sh APP_PID SECONDS INTERVAL OUT.jsonl' >&2; exit 2; }
pid=$1 secs=$2 interval=$3 out=$4
: >"$out"; end=$(( $(date +%s) + secs ))
while (( $(date +%s) < end )); do
  rss=$(awk '/^VmRSS:/{print $2*1024}' "/proc/$pid/status" 2>/dev/null || echo 0)
  pgpss=$(sudo -n bash -c 'for f in /proc/[0-9]*/comm; do read -r c < "$f" 2>/dev/null || continue; [[ $c == postgres ]] || continue; p=${f%/comm}; awk "/^Pss:/{s+=\$2} END{print s*1024}" "$p/smaps_rollup" 2>/dev/null || true; done' | awk '{s+=$1} END{print s+0}')
  printf '{"t":"%s","app_rss_bytes":%s,"postgres_pss_bytes":%s,"stack_bytes":%s}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$rss" "$pgpss" "$((rss+pgpss))" >>"$out"
  sleep "$interval"
done

