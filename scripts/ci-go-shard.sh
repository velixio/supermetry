#!/usr/bin/env bash
# Deterministic package sharding for CI.
#   scripts/ci-go-shard.sh <index> <count>   print the packages of shard <index> (1-based)
#   scripts/ci-go-shard.sh --verify <count>  assert the shards partition `go list ./...`
# Packages are assigned longest-first to the least-loaded shard, using
# scripts/ci-go-weights.txt ("<seconds> <package relative to module root>");
# unknown packages weigh 5.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
weights="$here/ci-go-weights.txt"
cd "$here/.."

shard() { # <index> <count>
  local module
  module="$(go list -m)"
  go list ./... | LC_ALL=C sort | awk -v module="$module" -v wfile="$weights" '
    BEGIN {
      while ((getline line < wfile) > 0) {
        if (line ~ /^[[:space:]]*(#|$)/) continue
        split(line, f, /[[:space:]]+/)
        w[f[2]] = f[1] + 0
      }
    }
    {
      rel = $0
      if (rel == module) rel = "."
      else sub("^" module "/", "", rel)
      # inverted zero-padded weight: a plain sort gives weight desc, then name asc
      printf "%09d %s\n", 999999999 - ((rel in w) ? w[rel] : 5), $0
    }' | LC_ALL=C sort | awk -v idx="$1" -v n="$2" '
    BEGIN { for (i = 1; i <= n; i++) load[i] = 0 }
    {
      best = 1
      for (i = 2; i <= n; i++) if (load[i] < load[best]) best = i
      load[best] += 999999999 - $1
      if (best == idx) print $2
    }'
}

if [[ "${1:-}" == "--verify" ]]; then
  count="${2:?usage: $0 --verify <count>}"
  expected="$(go list ./... | LC_ALL=C sort)"
  got="$(for i in $(seq 1 "$count"); do shard "$i" "$count"; done | LC_ALL=C sort)"
  if [[ "$expected" != "$got" ]]; then
    echo "shard verification failed: union of shards differs from go list ./..." >&2
    diff <(printf '%s\n' "$expected") <(printf '%s\n' "$got") >&2 || true
    exit 1
  fi
  echo "ok: $(printf '%s\n' "$expected" | wc -l) packages in $count shards, each exactly once"
  exit 0
fi

index="${1:?usage: $0 <index> <count> | --verify <count>}"
count="${2:?usage: $0 <index> <count> | --verify <count>}"
if ((index < 1 || index > count)); then
  echo "index must be in 1..$count" >&2
  exit 2
fi
shard "$index" "$count"
